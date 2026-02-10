package main

import (
	"log/slog"
	"sync"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/session"
)

const (
	sendPrefix = ";; "
)

type sessionCompactor struct {
	mu        sync.Mutex
	session   *session.Session
	compactor *compact.Compactor
	writer    *sessionWriter
}

func (sc *sessionCompactor) Update(sess *session.Session, comp *compact.Compactor) {
	sc.mu.Lock()
	sc.session = sess
	sc.compactor = comp
	sc.mu.Unlock()
}

func (sc *sessionCompactor) ShouldCompact(messages []agent.AgentMessage) bool {
	sc.mu.Lock()
	comp := sc.compactor
	sc.mu.Unlock()
	if comp == nil {
		return false
	}
	return comp.ShouldCompact(messages)
}

func (sc *sessionCompactor) Compact(messages []agent.AgentMessage) ([]agent.AgentMessage, error) {
	sc.mu.Lock()
	sess := sc.session
	comp := sc.compactor
	writer := sc.writer
	sc.mu.Unlock()
	if sess == nil || comp == nil {
		return messages, nil
	}
	if writer != nil {
		compacted, err := writer.Compact(sess, comp)
		if err != nil {
			return nil, err
		}
		if compacted != nil {
			return compacted, nil
		}
	}
	if _, err := sess.Compact(comp); err != nil {
		return nil, err
	}
	return sess.GetMessages(), nil
}

type sessionWriteRequest struct {
	sess     *session.Session
	message  *agent.AgentMessage
	comp     *compact.Compactor
	response chan sessionCompactResponse
}

type sessionCompactResponse struct {
	messages []agent.AgentMessage
	err      error
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
			if req.response != nil {
				var resp sessionCompactResponse
				if req.sess == nil || req.comp == nil {
					resp.messages = nil
				} else {
					if _, err := req.sess.Compact(req.comp); err != nil {
						resp.err = err
					} else {
						resp.messages = req.sess.GetMessages()
					}
				}
				req.response <- resp
				continue
			}
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

func (w *sessionWriter) Append(sess *session.Session, message agent.AgentMessage) {
	if w == nil || sess == nil {
		return
	}
	w.enqueue(sessionWriteRequest{sess: sess, message: &message})
}

func (w *sessionWriter) Compact(sess *session.Session, comp *compact.Compactor) ([]agent.AgentMessage, error) {
	if w == nil || sess == nil || comp == nil {
		return nil, nil
	}
	response := make(chan sessionCompactResponse, 1)
	if !w.enqueue(sessionWriteRequest{sess: sess, comp: comp, response: response}) {
		return nil, nil
	}
	result := <-response
	return result.messages, result.err
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
