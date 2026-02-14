package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/tiancaiamao/ai/pkg/llm"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

const (
	defaultAsyncToolSummaryMaxPendingBatches = 2
	defaultAsyncToolSummaryBatchSize         = 4
	defaultAsyncToolSummaryQueueSize         = 8
)

type asyncToolSummaryJob struct {
	keys     []string
	messages []AgentMessage
}

type asyncToolSummaryResult struct {
	keys     []string
	original []AgentMessage
	summary  string
}

type asyncToolSummarizer struct {
	ctx        context.Context
	cancel     context.CancelFunc
	strategy   string
	model      llm.Model
	apiKey     string
	cutoff     int
	maxPending int
	batchSize  int

	jobs    chan asyncToolSummaryJob
	results chan asyncToolSummaryResult

	mu             sync.Mutex
	queuedKeys     map[string]struct{}
	ready          []asyncToolSummaryResult
	pendingBatches int
	wg             sync.WaitGroup
}

func newAsyncToolSummarizer(parent context.Context, config *LoopConfig) *asyncToolSummarizer {
	if config == nil || config.ToolCallCutoff <= 0 {
		return nil
	}
	strategy := normalizeToolSummaryStrategy(config.ToolSummaryStrategy)
	if strategy == "off" {
		return nil
	}

	ctx, cancel := context.WithCancel(parent)
	s := &asyncToolSummarizer{
		ctx:        ctx,
		cancel:     cancel,
		strategy:   strategy,
		model:      config.Model,
		apiKey:     config.APIKey,
		cutoff:     config.ToolCallCutoff,
		maxPending: defaultAsyncToolSummaryMaxPendingBatches,
		batchSize:  defaultAsyncToolSummaryBatchSize,
		jobs:       make(chan asyncToolSummaryJob, defaultAsyncToolSummaryQueueSize),
		results:    make(chan asyncToolSummaryResult, defaultAsyncToolSummaryQueueSize),
		queuedKeys: make(map[string]struct{}),
	}

	s.wg.Add(1)
	go s.worker()
	return s
}

func (s *asyncToolSummarizer) Close() {
	if s == nil {
		return
	}
	s.cancel()
	s.wg.Wait()
}

func (s *asyncToolSummarizer) worker() {
	defer s.wg.Done()

	for {
		select {
		case <-s.ctx.Done():
			return
		case job := <-s.jobs:
			summarySpan := traceevent.StartSpan(s.ctx, "tool_summary_batch", traceevent.CategoryTool,
				traceevent.Field{Key: "mode", Value: "batch"},
				traceevent.Field{Key: "strategy", Value: s.strategy},
				traceevent.Field{Key: "batch_size", Value: len(job.messages)},
			)
			summary := ""
			fallback := false
			if s.strategy == "heuristic" {
				summary = fallbackToolSummaryBatch(job.messages)
				fallback = true
			} else {
				text, err := summarizeToolResultsBatchFn(s.ctx, s.model, s.apiKey, job.messages)
				if err != nil {
					summary = fallbackToolSummaryBatch(job.messages)
					fallback = true
					summarySpan.AddField("llm_error", err.Error())
				} else {
					summary = text
				}
			}
			summarySpan.AddField("fallback", fallback)
			summarySpan.AddField("summary_chars", len([]rune(summary)))
			summarySpan.End()

			result := asyncToolSummaryResult{
				keys:     append([]string(nil), job.keys...),
				original: append([]AgentMessage(nil), job.messages...),
				summary:  summary,
			}

			select {
			case s.results <- result:
			case <-s.ctx.Done():
				return
			}
		}
	}
}

func (s *asyncToolSummarizer) collectReady() {
	if s == nil {
		return
	}
	for {
		select {
		case result := <-s.results:
			s.mu.Lock()
			s.ready = append(s.ready, result)
			if s.pendingBatches > 0 {
				s.pendingBatches--
			}
			s.mu.Unlock()
		default:
			return
		}
	}
}

func (s *asyncToolSummarizer) schedule(agentCtx *AgentContext) {
	if s == nil || agentCtx == nil {
		return
	}

	s.collectReady()
	s.pruneStale(agentCtx)

	visible := 0
	candidates := make([]AgentMessage, 0)

	s.mu.Lock()
	queuedSnapshot := make(map[string]struct{}, len(s.queuedKeys))
	for k := range s.queuedKeys {
		queuedSnapshot[k] = struct{}{}
	}
	pendingBatches := s.pendingBatches
	s.mu.Unlock()

	for _, msg := range agentCtx.Messages {
		if msg.Role != "toolResult" || !msg.IsAgentVisible() {
			continue
		}
		visible++
		key := toolResultKey(msg)
		if key == "" {
			continue
		}
		if _, exists := queuedSnapshot[key]; exists {
			continue
		}
		candidates = append(candidates, msg)
	}

	need := visible - s.cutoff
	if need <= 0 {
		return
	}

	availableBatches := s.maxPending - pendingBatches
	if availableBatches <= 0 {
		return
	}

	for need > 0 && availableBatches > 0 && len(candidates) > 0 {
		size := minInt(need, s.batchSize)
		size = minInt(size, len(candidates))
		if size <= 0 {
			return
		}

		batch := append([]AgentMessage(nil), candidates[:size]...)
		candidates = candidates[size:]
		keys := make([]string, 0, len(batch))
		for _, msg := range batch {
			if key := toolResultKey(msg); key != "" {
				keys = append(keys, key)
			}
		}
		if len(keys) == 0 {
			need -= size
			availableBatches--
			continue
		}

		s.mu.Lock()
		alreadyQueued := false
		for _, key := range keys {
			if _, exists := s.queuedKeys[key]; exists {
				alreadyQueued = true
				break
			}
		}
		if alreadyQueued {
			s.mu.Unlock()
			need -= size
			continue
		}
		for _, key := range keys {
			s.queuedKeys[key] = struct{}{}
		}
		s.pendingBatches++
		s.mu.Unlock()

		job := asyncToolSummaryJob{keys: keys, messages: batch}
		select {
		case s.jobs <- job:
		default:
			s.mu.Lock()
			for _, key := range keys {
				delete(s.queuedKeys, key)
			}
			if s.pendingBatches > 0 {
				s.pendingBatches--
			}
			s.mu.Unlock()
			return
		case <-s.ctx.Done():
			s.mu.Lock()
			for _, key := range keys {
				delete(s.queuedKeys, key)
			}
			if s.pendingBatches > 0 {
				s.pendingBatches--
			}
			s.mu.Unlock()
			return
		}

		need -= size
		availableBatches--
	}
}

func (s *asyncToolSummarizer) applyReady(agentCtx *AgentContext) {
	if s == nil || agentCtx == nil {
		return
	}

	s.collectReady()
	s.pruneStale(agentCtx)

	s.mu.Lock()
	if len(s.ready) == 0 {
		s.mu.Unlock()
		return
	}
	ready := append([]asyncToolSummaryResult(nil), s.ready...)
	s.ready = nil
	s.mu.Unlock()

	for _, batch := range ready {
		applySpan := traceevent.StartSpan(s.ctx, "tool_summary_batch", traceevent.CategoryTool,
			traceevent.Field{Key: "mode", Value: "batch_apply"},
			traceevent.Field{Key: "batch_size", Value: len(batch.keys)},
		)
		archived := make([]AgentMessage, 0, len(batch.keys))
		for _, key := range batch.keys {
			for i, msg := range agentCtx.Messages {
				if msg.Role != "toolResult" || !msg.IsAgentVisible() {
					continue
				}
				if toolResultKey(msg) != key {
					continue
				}
				agentCtx.Messages[i] = archiveToolResult(msg)
				archived = append(archived, msg)
				break
			}
		}

		if len(archived) > 0 {
			agentCtx.Messages = append(agentCtx.Messages, newToolBatchSummaryMessage(archived, batch.summary))
		}
		applySpan.AddField("archived_count", len(archived))
		applySpan.AddField("summary_chars", len([]rune(batch.summary)))
		applySpan.End()

		s.mu.Lock()
		for _, key := range batch.keys {
			delete(s.queuedKeys, key)
		}
		s.mu.Unlock()
	}
}

func (s *asyncToolSummarizer) pruneStale(agentCtx *AgentContext) {
	if s == nil || agentCtx == nil {
		return
	}

	live := make(map[string]struct{})
	for _, msg := range agentCtx.Messages {
		if msg.Role != "toolResult" || !msg.IsAgentVisible() {
			continue
		}
		key := toolResultKey(msg)
		if key != "" {
			live[key] = struct{}{}
		}
	}

	s.mu.Lock()
	for key := range s.queuedKeys {
		if _, ok := live[key]; !ok {
			delete(s.queuedKeys, key)
		}
	}
	s.mu.Unlock()
}

func toolResultKey(msg AgentMessage) string {
	if msg.Role != "toolResult" {
		return ""
	}
	if id := strings.TrimSpace(msg.ToolCallID); id != "" {
		return id
	}
	name := strings.TrimSpace(msg.ToolName)
	if name == "" {
		name = "unknown"
	}
	return fmt.Sprintf("%s#%d", name, msg.Timestamp)
}
