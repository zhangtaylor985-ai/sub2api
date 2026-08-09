package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseECBExchangeRateCSV(t *testing.T) {
	quote, err := parseECBExchangeRateCSV(strings.NewReader(`KEY,FREQ,CURRENCY,TIME_PERIOD,OBS_VALUE
EXR.D.CNY.EUR.SP00.A,D,CNY,2026-08-07,7.7834
EXR.D.USD.EUR.SP00.A,D,USD,2026-08-07,1.1535
`))
	require.NoError(t, err)
	require.Equal(t, int64(6_747_638), quote.RateScaled)
	require.Equal(t, "2026-08-07", quote.ObservedOn.Format("2006-01-02"))
	require.Equal(t, "ECB", quote.Source)
}

func TestBusinessServiceRefreshCurrentExchangeRateUsesECBAndFallback(t *testing.T) {
	initBusinessTestTimezone(t)
	now := businessTestTime(2026, time.August, 9, 9, 0)
	repo := newBusinessManagementRepositoryStub()
	svc := NewBusinessService(repo)
	svc.now = func() time.Time { return now }
	svc.exchangeRateFetcher = businessExchangeRateFetcherStub{quote: BusinessExchangeRateQuote{
		RateScaled: 6_747_638,
		ObservedOn: time.Date(2026, time.August, 7, 0, 0, 0, 0, time.UTC),
		Source:     "ECB",
	}}

	result, err := svc.RefreshCurrentExchangeRate(context.Background())
	require.NoError(t, err)
	require.False(t, result.UsedFallback)
	require.Equal(t, int64(6_747_638), result.Rate.RateScaled)
	require.Equal(t, "ECB", result.Rate.Source)
	require.NotNil(t, repo.upsertedRate)

	svc.exchangeRateFetcher = businessExchangeRateFetcherStub{err: errors.New("temporary outage")}
	retained, err := svc.RefreshCurrentExchangeRate(context.Background())
	require.NoError(t, err)
	require.True(t, retained.UsedFallback)
	require.Equal(t, int64(6_747_638), retained.Rate.RateScaled)

	repo.exchangeRates = nil
	fallback, err := svc.RefreshCurrentExchangeRate(context.Background())
	require.NoError(t, err)
	require.True(t, fallback.UsedFallback)
	require.Equal(t, defaultBusinessUSDCNYFallbackRate, fallback.Rate.RateScaled)
	require.Equal(t, "fallback", fallback.Rate.Source)
}

type businessExchangeRateFetcherStub struct {
	quote BusinessExchangeRateQuote
	err   error
}

func (f businessExchangeRateFetcherStub) FetchUSDCNY(context.Context) (BusinessExchangeRateQuote, error) {
	return f.quote, f.err
}
