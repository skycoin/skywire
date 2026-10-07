// Package store pkg/deployment/tpd/store/redis_batched_pipe.go c4-net-discovery
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-redis/redis/v8"
)

// pipelineBatch bounds the commands in one pipeline. A whole metrics window
// read in one pipeline is a reply that outlasts the client's 5s read timeout,
// which fails every command in it, and each failed read looked like a key with
// no data: every settled day came out empty.
const pipelineBatch = 5000

// batchedPipe queues commands and runs them pipelineBatch at a time.
type batchedPipe struct {
	ctx    context.Context
	client *redis.Client
	pipe   redis.Pipeliner
	n      int
	err    error
}

func (s *redisStore) batchedPipe(ctx context.Context) *batchedPipe {
	return &batchedPipe{ctx: ctx, client: s.client, pipe: s.client.Pipeline()}
}

// next returns the pipeline the next command goes on.
func (b *batchedPipe) next() redis.Pipeliner {
	if b.n == pipelineBatch {
		b.flush()
	}
	b.n++
	return b.pipe
}

func (b *batchedPipe) flush() {
	if b.n > 0 && b.err == nil {
		if _, err := b.pipe.Exec(b.ctx); err != nil && !errors.Is(err, redis.Nil) {
			b.err = fmt.Errorf("redis pipeline: %w", err)
		}
	}
	b.pipe = b.client.Pipeline()
	b.n = 0
}

// exec runs what is queued and reports the first batch that failed.
func (b *batchedPipe) exec() error {
	b.flush()
	return b.err
}
