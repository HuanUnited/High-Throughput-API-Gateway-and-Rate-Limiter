package storage

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockClientStore struct {
	calls atomic.Int64
	limit int
	err   error
}

func (m *mockClientStore) GetClientLimit(_ context.Context, _ string) (int, error) {
	m.calls.Add(1)
	return m.limit, m.err
}

func TestCachedStore_GetClientLimit(t *testing.T) {
	ctx := context.Background()

	t.Run("empty API key returns ErrInvalidAPIKey", func(t *testing.T) {
		mock := &mockClientStore{limit: 100}
		cs := NewCachedStore(mock, time.Minute)
		defer func() { _ = cs.Close() }()

		_, err := cs.GetClientLimit(ctx, "")
		require.ErrorIs(t, err, ErrInvalidAPIKey)
		assert.Equal(t, int64(0), mock.calls.Load())
	})

	t.Run("caches successful lookups", func(t *testing.T) {
		mock := &mockClientStore{limit: 100}
		cs := NewCachedStore(mock, time.Minute)
		defer func() { _ = cs.Close() }()

		limit1, err := cs.GetClientLimit(ctx, "client-a")
		require.NoError(t, err)
		assert.Equal(t, 100, limit1)

		limit2, err := cs.GetClientLimit(ctx, "client-a")
		require.NoError(t, err)
		assert.Equal(t, 100, limit2)

		assert.Equal(t, int64(1), mock.calls.Load())
	})

	t.Run("expires entries after TTL", func(t *testing.T) {
		mock := &mockClientStore{limit: 50}
		cs := NewCachedStore(mock, 20*time.Millisecond)
		defer func() { _ = cs.Close() }()

		_, err := cs.GetClientLimit(ctx, "client-ttl")
		require.NoError(t, err)
		assert.Equal(t, int64(1), mock.calls.Load())

		time.Sleep(35 * time.Millisecond)

		_, err = cs.GetClientLimit(ctx, "client-ttl")
		require.NoError(t, err)
		assert.Equal(t, int64(2), mock.calls.Load())
	})

	t.Run("invalidate removes entry", func(t *testing.T) {
		mock := &mockClientStore{limit: 200}
		cs := NewCachedStore(mock, time.Minute)
		defer func() { _ = cs.Close() }()

		_, err := cs.GetClientLimit(ctx, "client-inv")
		require.NoError(t, err)
		assert.Equal(t, int64(1), mock.calls.Load())

		cs.Invalidate("client-inv")

		_, err = cs.GetClientLimit(ctx, "client-inv")
		require.NoError(t, err)
		assert.Equal(t, int64(2), mock.calls.Load())
	})

	t.Run("propagates underlying store errors", func(t *testing.T) {
		mock := &mockClientStore{err: errors.New("db connection failure")}
		cs := NewCachedStore(mock, time.Minute)
		defer func() { _ = cs.Close() }()

		_, err := cs.GetClientLimit(ctx, "client-err")
		require.Error(t, err)
		assert.Equal(t, int64(1), mock.calls.Load())
	})
}
