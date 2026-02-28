package main

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"errors"
	"log/slog"
	"reflect"
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

func (sc *sessionCompactor) ShouldCompact(messages []agentctx.AgentMessage) bool {
	sc.mu.Lock()
	sess := sc.session
	comp := sc.compactor
	sc.mu.Unlock()
	if comp == nil {
		return false
	}
	if !comp.ShouldCompact(messages) {
		return false
	}
	if sess == nil {
		return false
	}
	return sess.CanCompact(comp)
}

func (sc *sessionCompactor) Compact(messages []agentctx.AgentMessage, previousSummary string) (*agent.CompactionResult, error) {
	sc.mu.Lock()
	sess := sc.session
	comp := sc.compactor
	writer := sc.writer
	sc.mu.Unlock()
	if sess == nil || comp == nil {
		return &agent.CompactionResult{Messages: messages}, nil
	}
	if writer != nil {
		compacted, err := writer.Compact(sess, comp)
		if err != nil {
			if session.IsNonActionableCompactionError(err) {
				return &agent.CompactionResult{Messages: messages}, nil
			}
			return nil, err
		}
		if compacted != nil {
			return compacted, nil
		}
	}
	// Session layer handles previousSummary internally via compaction entries
	sessionResult, err := sess.Compact(comp)
	if err != nil {
		if session.IsNonActionableCompactionError(err) {
			return &agent.CompactionResult{Messages: messages}, nil
		}
		return nil, err
	}
	// Convert session.CompactionResult to agent.CompactionResult
	result := &agent.CompactionResult{
		Summary:      sessionResult.Summary,
		Messages:     sess.GetMessages(),
		TokensBefore: sessionResult.TokensBefore,
		TokensAfter:  sessionResult.TokensAfter,
	}
	return result, nil
}

type sessionWriteRequest struct {
	sess            *session.Session
	message         *agentctx.AgentMessage
	replaceMessages []agentctx.AgentMessage
	comp            *compact.Compactor
	response        chan sessionCompactResponse
	replaceResp     chan error
}

type sessionCompactResponse struct {
	messages []agentctx.AgentMessage
	summary  string
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
					result, err := req.sess.Compact(req.comp)
					if err != nil {
						if session.IsNonActionableCompactionError(err) || errors.Is(err, session.ErrNothingToCompact) {
							resp.messages = req.sess.GetMessages()
							req.response <- resp
							continue
						}
						resp.err = err
					} else {
						resp.messages = req.sess.GetMessages()
						resp.summary = result.Summary
					}
				}
				req.response <- resp
				continue
			}
			if req.replaceMessages != nil {
				var err error
				if req.sess != nil {
					current := req.sess.GetMessages()
					if !reflect.DeepEqual(current, req.replaceMessages) {
						err = req.sess.SaveMessages(req.replaceMessages)
					}
				}
				if req.replaceResp != nil {
					req.replaceResp <- err
				}
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

func (w *sessionWriter) Append(sess *session.Session, message agentctx.AgentMessage) {
	if w == nil || sess == nil {
		return
	}
	w.enqueue(sessionWriteRequest{sess: sess, message: &message})
}

func (w *sessionWriter) Compact(sess *session.Session, comp *compact.Compactor) (*agent.CompactionResult, error) {
	if w == nil || sess == nil || comp == nil {
		return nil, nil
	}
	response := make(chan sessionCompactResponse, 1)
	if !w.enqueue(sessionWriteRequest{sess: sess, comp: comp, response: response}) {
		return nil, nil
	}
	result := <-response
	if result.err != nil {
		return nil, result.err
	}
	return &agent.CompactionResult{
		Summary:  result.summary,
		Messages: result.messages,
	}, nil
}

func (w *sessionWriter) Replace(sess *session.Session, messages []agentctx.AgentMessage) error {
	if w == nil || sess == nil {
		return nil
	}
	response := make(chan error, 1)
	req := sessionWriteRequest{
		sess:            sess,
		replaceMessages: append([]agentctx.AgentMessage(nil), messages...),
		replaceResp:     response,
	}
	if !w.enqueue(req) {
		return nil
	}
	return <-response
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
