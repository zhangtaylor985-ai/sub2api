package service

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ecbExchangeRateURL                = "https://data-api.ecb.europa.eu/service/data/EXR/D.USD+CNY.EUR.SP00.A?lastNObservations=1&format=csvdata"
	defaultBusinessFXRefreshInterval  = 12 * time.Hour
	defaultBusinessFXRequestTimeout   = 15 * time.Second
	defaultBusinessUSDCNYFallbackRate = int64(6_750_000)
	maxECBExchangeRateResponseBytes   = 1 << 20
)

type BusinessExchangeRateQuote struct {
	RateScaled int64
	ObservedOn time.Time
	Source     string
}

type BusinessExchangeRateFetcher interface {
	FetchUSDCNY(ctx context.Context) (BusinessExchangeRateQuote, error)
}

type ECBExchangeRateFetcher struct {
	client *http.Client
	url    string
}

func NewECBExchangeRateFetcher(client *http.Client) *ECBExchangeRateFetcher {
	if client == nil {
		client = &http.Client{Timeout: defaultBusinessFXRequestTimeout}
	}
	return &ECBExchangeRateFetcher{client: client, url: ecbExchangeRateURL}
}

func (f *ECBExchangeRateFetcher) FetchUSDCNY(
	ctx context.Context,
) (BusinessExchangeRateQuote, error) {
	if f == nil || f.client == nil {
		return BusinessExchangeRateQuote{}, errors.New("ECB exchange rate client is unavailable")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	if err != nil {
		return BusinessExchangeRateQuote{}, fmt.Errorf("build ECB exchange rate request: %w", err)
	}
	req.Header.Set("Accept", "text/csv")
	resp, err := f.client.Do(req)
	if err != nil {
		return BusinessExchangeRateQuote{}, fmt.Errorf("fetch ECB exchange rates: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return BusinessExchangeRateQuote{}, fmt.Errorf("fetch ECB exchange rates: unexpected status %d", resp.StatusCode)
	}
	return parseECBExchangeRateCSV(io.LimitReader(resp.Body, maxECBExchangeRateResponseBytes))
}

func parseECBExchangeRateCSV(reader io.Reader) (BusinessExchangeRateQuote, error) {
	csvReader := csv.NewReader(reader)
	header, err := csvReader.Read()
	if err != nil {
		return BusinessExchangeRateQuote{}, fmt.Errorf("read ECB exchange rate header: %w", err)
	}
	columns := make(map[string]int, len(header))
	for index, name := range header {
		columns[strings.TrimSpace(name)] = index
	}
	currencyIndex, currencyOK := columns["CURRENCY"]
	periodIndex, periodOK := columns["TIME_PERIOD"]
	valueIndex, valueOK := columns["OBS_VALUE"]
	if !currencyOK || !periodOK || !valueOK {
		return BusinessExchangeRateQuote{}, errors.New("ECB exchange rate response is missing required columns")
	}

	rates := make(map[string]float64, 2)
	var observedOn time.Time
	for {
		record, readErr := csvReader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return BusinessExchangeRateQuote{}, fmt.Errorf("read ECB exchange rate row: %w", readErr)
		}
		if currencyIndex >= len(record) || periodIndex >= len(record) || valueIndex >= len(record) {
			continue
		}
		currency := strings.ToUpper(strings.TrimSpace(record[currencyIndex]))
		if currency != "USD" && currency != "CNY" {
			continue
		}
		period, parseErr := time.Parse("2006-01-02", strings.TrimSpace(record[periodIndex]))
		if parseErr != nil {
			return BusinessExchangeRateQuote{}, fmt.Errorf("parse ECB observation date: %w", parseErr)
		}
		if !observedOn.IsZero() && !sameBusinessDate(observedOn, period) {
			return BusinessExchangeRateQuote{}, errors.New("ECB USD and CNY observations use different dates")
		}
		value, parseErr := strconv.ParseFloat(strings.TrimSpace(record[valueIndex]), 64)
		if parseErr != nil || value <= 0 {
			return BusinessExchangeRateQuote{}, errors.New("ECB exchange rate contains an invalid value")
		}
		observedOn = period
		rates[currency] = value
	}
	usd, usdOK := rates["USD"]
	cny, cnyOK := rates["CNY"]
	if !usdOK || !cnyOK || usd <= 0 || cny <= 0 {
		return BusinessExchangeRateQuote{}, errors.New("ECB response does not contain both USD and CNY rates")
	}
	rateScaled := int64(math.Round(cny / usd * float64(BusinessRateScale)))
	if rateScaled <= 0 || rateScaled > businessMaxRateScaled {
		return BusinessExchangeRateQuote{}, errors.New("derived ECB USD/CNY rate is outside the supported range")
	}
	return BusinessExchangeRateQuote{
		RateScaled: rateScaled,
		ObservedOn: observedOn,
		Source:     "ECB",
	}, nil
}

type BusinessExchangeRateRefreshResult struct {
	Rate         BusinessExchangeRate `json:"rate"`
	UsedFallback bool                 `json:"used_fallback"`
}

func (s *BusinessService) RefreshCurrentExchangeRate(
	ctx context.Context,
) (*BusinessExchangeRateRefreshResult, error) {
	repo, err := s.managementRepository()
	if err != nil {
		return nil, err
	}
	month := businessMonthStart(s.now())
	quote := BusinessExchangeRateQuote{}
	var fetchErr error
	if s.exchangeRateFetcher == nil {
		fetchErr = errors.New("business exchange rate fetcher is unavailable")
	} else {
		quote, fetchErr = s.exchangeRateFetcher.FetchUSDCNY(ctx)
	}
	if fetchErr == nil {
		notes := fmt.Sprintf(
			"ECB reference rate observed on %s; USD/CNY derived from EUR/CNY divided by EUR/USD.",
			quote.ObservedOn.Format("2006-01-02"),
		)
		rate := &BusinessExchangeRate{
			Month: month, Currency: "USD", RateScaled: quote.RateScaled,
			Source: quote.Source, Notes: &notes,
		}
		if err := repo.UpsertExchangeRate(ctx, rate); err != nil {
			return nil, fmt.Errorf("store automatic business exchange rate: %w", err)
		}
		return &BusinessExchangeRateRefreshResult{Rate: *rate}, nil
	}

	existing, listErr := repo.ListExchangeRates(ctx, month)
	if listErr != nil {
		return nil, fmt.Errorf("load exchange rate fallback: %w", listErr)
	}
	for i := range existing {
		if strings.EqualFold(existing[i].Currency, "USD") {
			return &BusinessExchangeRateRefreshResult{Rate: existing[i], UsedFallback: true}, nil
		}
	}
	notes := "Fixed USD/CNY fallback used because the ECB reference rate was unavailable."
	rate := &BusinessExchangeRate{
		Month: month, Currency: "USD", RateScaled: defaultBusinessUSDCNYFallbackRate,
		Source: "fallback", Notes: &notes,
	}
	if err := repo.UpsertExchangeRate(ctx, rate); err != nil {
		return nil, fmt.Errorf("store fallback business exchange rate: %w", err)
	}
	return &BusinessExchangeRateRefreshResult{Rate: *rate, UsedFallback: true}, nil
}

type BusinessExchangeRateRefresher struct {
	service  *BusinessService
	interval time.Duration

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	wg        sync.WaitGroup
}

func NewBusinessExchangeRateRefresher(
	service *BusinessService,
	interval time.Duration,
) *BusinessExchangeRateRefresher {
	if interval <= 0 {
		interval = defaultBusinessFXRefreshInterval
	}
	return &BusinessExchangeRateRefresher{
		service: service, interval: interval, stopCh: make(chan struct{}),
	}
}

func ProvideBusinessExchangeRateRefresher(service *BusinessService) *BusinessExchangeRateRefresher {
	refresher := NewBusinessExchangeRateRefresher(service, defaultBusinessFXRefreshInterval)
	refresher.Start()
	return refresher
}

func (r *BusinessExchangeRateRefresher) Start() {
	if r == nil || r.service == nil {
		return
	}
	r.startOnce.Do(func() {
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			ticker := time.NewTicker(r.interval)
			defer ticker.Stop()
			r.runAndLog()
			for {
				select {
				case <-ticker.C:
					r.runAndLog()
				case <-r.stopCh:
					return
				}
			}
		}()
		log.Printf("[BusinessExchangeRateRefresher] Started")
	})
}

func (r *BusinessExchangeRateRefresher) Stop() {
	if r == nil {
		return
	}
	r.stopOnce.Do(func() { close(r.stopCh) })
	r.wg.Wait()
}

func (r *BusinessExchangeRateRefresher) runAndLog() {
	ctx, cancel := context.WithTimeout(context.Background(), defaultBusinessFXRequestTimeout+5*time.Second)
	defer cancel()
	result, err := r.service.RefreshCurrentExchangeRate(ctx)
	if err != nil {
		log.Printf("[BusinessExchangeRateRefresher] Refresh failed: %v", err)
		return
	}
	log.Printf(
		"[BusinessExchangeRateRefresher] Refresh completed: source=%s rate=%.6f fallback=%t",
		result.Rate.Source,
		float64(result.Rate.RateScaled)/float64(BusinessRateScale),
		result.UsedFallback,
	)
}
