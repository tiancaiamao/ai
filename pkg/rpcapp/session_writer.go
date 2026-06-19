package rpcapp

import (
	"context"
	"log/slog"
	"sync"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/session"
)

// sessionCompactor is a thin mutable wrapper around *compact.Compactor.
// It exists so the loop config can hold a stable Compactor reference
// that can be swapped on model/session changes without rebuilding the config.
type sessionCompactor struct {
	mu        sync.Mutex
	compactor agentctx.Compactor
}

func (sc *sessionCompactor) Update(comp agentctx.Compactor) {
	sc.mu.Lock()
	sc.compactor = comp
	sc.mu.Unlock()
}

func (sc *sessionCompactor) ShouldCompact(ctx context.Context, agentCtx *agentctx.AgentContext) bool {
	sc.mu.Lock()
	comp := sc.compactor
	sc.mu.Unlock()
	if comp == nil {
		return false
	}
	return comp.ShouldCompact(ctx, agentCtx)
}

// Compact delegates directly to the underlying compactor.
// The compactor modifies agentCtx.RecentMessages in place.
// Session persistence is handled separately (via events or direct writer.Replace).
func (sc *sessionCompactor) Compact(ctx context.Context, agentCtx *agentctx.AgentContext) (*agentctx.CompactionResult, error) {
	sc.mu.Lock()
	comp := sc.compactor
	sc.mu.Unlock()
	if comp == nil {
		return nil, nil
	}
	return comp.Compact(ctx, agentCtx)
}

func (sc *sessionCompactor) CalculateDynamicThreshold() int {
	sc.mu.Lock()
	comp := sc.compactor
	sc.mu.Unlock()
	if comp == nil {
		return 0
	}
	return comp.CalculateDynamicThreshold()
}

// --- sessionWriter: single-goroutine serializer for session writes ---

type sessionWriteRequest struct {
	sess    *session.Session
	message *agentctx.AgentMessage
}

type sessionWriter struct {
	mu     sync.Mutex
	closed bool
	ch     chan sessionWriteRequest
	wg     sync.WaitGroup
}

func newSessionWriter(buffer int) *sessionWriter {
	writer := &sessionWriter{
		ch: make(chan sessionWriteRequest, buffer),
	}
	writer.wg.Add(1)
	go func() {
		defer writer.wg.Done()
		for req := range writer.ch {
			if req.sess == nil || req.message == nil {
				continue
			}
			if _, err := req.sess.AppendMessage(*req.message); err != nil {
				slog.Info("Failed to append session message:", "value", err)
			}
		}
	}()
	return writer
}

func (w *sessionWriter) Append(sess *session.Session, message agentctx.AgentMessage) {
	if w == nil || sess == nil {
		return
	}
	w.enqueue(sessionWriteRequest{sess: sess, message: &message})
}

func (w *sessionWriter) Close() {
	if w == nil {
		return
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.closed = true
	close(w.ch)
	w.mu.Unlock()
	w.wg.Wait()
}

func (w *sessionWriter) enqueue(req sessionWriteRequest) bool {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return false
	}
	ch := w.ch
	w.mu.Unlock()
	ch <- req
	return true
}
