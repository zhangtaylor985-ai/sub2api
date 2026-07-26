//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type openAIMessagesDispatchSettingRepoStub struct {
	value string
	err   error
	calls int
}

func (s *openAIMessagesDispatchSettingRepoStub) Get(context.Context, string) (*Setting, error) {
	panic("unexpected Get call")
}

func (s *openAIMessagesDispatchSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	s.calls++
	if key != SettingKeyOpenAIMessagesDispatchDefaultTarget {
		panic("unexpected setting key: " + key)
	}
	return s.value, s.err
}

func (s *openAIMessagesDispatchSettingRepoStub) Set(context.Context, string, string) error {
	panic("unexpected Set call")
}

func (s *openAIMessagesDispatchSettingRepoStub) GetMultiple(context.Context, []string) (map[string]string, error) {
	panic("unexpected GetMultiple call")
}

func (s *openAIMessagesDispatchSettingRepoStub) SetMultiple(context.Context, map[string]string) error {
	panic("unexpected SetMultiple call")
}

func (s *openAIMessagesDispatchSettingRepoStub) GetAll(context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

func (s *openAIMessagesDispatchSettingRepoStub) Delete(context.Context, string) error {
	panic("unexpected Delete call")
}

func TestGetOpenAIMessagesDispatchDefaultTarget(t *testing.T) {
	t.Run("uses stored GPT56 target and caches it", func(t *testing.T) {
		repo := &openAIMessagesDispatchSettingRepoStub{value: "gpt-5.6-sol"}
		svc := NewSettingService(repo, &config.Config{})

		require.Equal(t, "gpt-5.6-sol", svc.GetOpenAIMessagesDispatchDefaultTarget(context.Background()))
		require.Equal(t, "gpt-5.6-sol", svc.GetOpenAIMessagesDispatchDefaultTarget(context.Background()))
		require.Equal(t, 1, repo.calls)
	})

	t.Run("missing setting uses product default", func(t *testing.T) {
		repo := &openAIMessagesDispatchSettingRepoStub{err: ErrSettingNotFound}
		svc := NewSettingService(repo, &config.Config{})

		require.Equal(t, DefaultOpenAIMessagesDispatchTarget, svc.GetOpenAIMessagesDispatchDefaultTarget(context.Background()))
	})

	t.Run("repository failure uses safe fallback", func(t *testing.T) {
		repo := &openAIMessagesDispatchSettingRepoStub{err: errors.New("database unavailable")}
		svc := NewSettingService(repo, &config.Config{})

		require.Equal(t, SafeOpenAIMessagesDispatchTarget, svc.GetOpenAIMessagesDispatchDefaultTarget(context.Background()))
	})

	t.Run("invalid stored target uses safe fallback", func(t *testing.T) {
		repo := &openAIMessagesDispatchSettingRepoStub{value: "gpt-5.6-terra"}
		svc := NewSettingService(repo, &config.Config{})

		require.Equal(t, SafeOpenAIMessagesDispatchTarget, svc.GetOpenAIMessagesDispatchDefaultTarget(context.Background()))
	})
}

func TestParseSettingsOpenAIMessagesDispatchDefaultTarget(t *testing.T) {
	svc := NewSettingService(&openAIMessagesDispatchSettingRepoStub{}, &config.Config{})

	require.Equal(t, DefaultOpenAIMessagesDispatchTarget, svc.parseSettings(map[string]string{}).OpenAIMessagesDispatchDefaultTarget)
	require.Equal(t, "gpt-5.4", svc.parseSettings(map[string]string{
		SettingKeyOpenAIMessagesDispatchDefaultTarget: "gpt-5.4",
	}).OpenAIMessagesDispatchDefaultTarget)
	require.Equal(t, SafeOpenAIMessagesDispatchTarget, svc.parseSettings(map[string]string{
		SettingKeyOpenAIMessagesDispatchDefaultTarget: "gpt-5.6-terra",
	}).OpenAIMessagesDispatchDefaultTarget)
}
