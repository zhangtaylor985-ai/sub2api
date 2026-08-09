package service

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

const (
	defaultBusinessSnapshotCheckInterval = time.Hour
	businessSnapshotCloseTimeout         = 2 * time.Minute
)

type BusinessSnapshotSchedulerRunResult struct {
	Month   string `json:"month,omitempty"`
	Skipped bool   `json:"skipped"`
	Created bool   `json:"created"`
}

// BusinessSnapshotScheduler closes the previous operating month on the first
// calendar day in the configured business timezone. CloseMonth is idempotent,
// so process restarts and repeated ticks are safe.
type BusinessSnapshotScheduler struct {
	service  *BusinessService
	interval time.Duration
	now      func() time.Time

	startOnce sync.Once
	stopOnce  sync.Once
	runMu     sync.Mutex
	stopCh    chan struct{}
	wg        sync.WaitGroup
}

func NewBusinessSnapshotScheduler(
	service *BusinessService,
	interval time.Duration,
) *BusinessSnapshotScheduler {
	if interval <= 0 {
		interval = defaultBusinessSnapshotCheckInterval
	}
	return &BusinessSnapshotScheduler{
		service:  service,
		interval: interval,
		now:      timezone.Now,
		stopCh:   make(chan struct{}),
	}
}

func (s *BusinessSnapshotScheduler) Start() {
	if s == nil || s.service == nil {
		return
	}
	s.startOnce.Do(func() {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			ticker := time.NewTicker(s.interval)
			defer ticker.Stop()
			s.runAndLog()
			for {
				select {
				case <-ticker.C:
					s.runAndLog()
				case <-s.stopCh:
					return
				}
			}
		}()
		log.Printf("[BusinessSnapshotScheduler] Started")
	})
}

func (s *BusinessSnapshotScheduler) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
}

func (s *BusinessSnapshotScheduler) runAndLog() {
	ctx, cancel := context.WithTimeout(context.Background(), businessSnapshotCloseTimeout)
	defer cancel()
	result, err := s.runOnce(ctx)
	if err != nil {
		log.Printf("[BusinessSnapshotScheduler] Close failed: %v", err)
		return
	}
	if !result.Skipped {
		log.Printf(
			"[BusinessSnapshotScheduler] Close completed: month=%s created=%t",
			result.Month,
			result.Created,
		)
	}
}

func (s *BusinessSnapshotScheduler) runOnce(
	ctx context.Context,
) (BusinessSnapshotSchedulerRunResult, error) {
	result := BusinessSnapshotSchedulerRunResult{Skipped: true}
	if s == nil || s.service == nil {
		return result, nil
	}
	s.runMu.Lock()
	defer s.runMu.Unlock()

	now := s.now().In(timezone.Location())
	if now.Day() != 1 {
		return result, nil
	}
	month := businessMonthStart(now.AddDate(0, -1, 0)).Format(businessMonthLayout)
	report, created, err := s.service.CloseMonth(ctx, CloseBusinessMonthInput{
		Month:       month,
		DataQuality: BusinessDataQualityActual,
	})
	if err != nil {
		return result, err
	}
	result.Month = report.Month.Format(businessMonthLayout)
	result.Skipped = false
	result.Created = created
	return result, nil
}
