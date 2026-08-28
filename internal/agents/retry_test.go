package agents

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// flakyInner fails its first N calls before yielding any chunk, then
// succeeds with the given chunks.
type flakyInner struct {
	chunks []string
	calls  int
	fails  int // number of initial calls that fail
}

func (f *flakyInner) Name() string { return "flaky" }

func (f *flakyInner) GenerateContent(_ context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		f.calls++
		if f.calls <= f.fails {
			yield(nil, errors.New("connection reset"))
			return
		}
		for _, c := range f.chunks {
			if !yield(&model.LLMResponse{Content: &genai.Content{Parts: []*genai.Part{{Text: c}}}}, nil) {
				return
			}
		}
	}
}

// failingAfterChunks yields the given chunks, then a transient error.
type failingAfterChunks struct {
	chunks []string
	calls  int
}

func (f *failingAfterChunks) Name() string { return "failing" }

func (f *failingAfterChunks) GenerateContent(_ context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		f.calls++
		for _, c := range f.chunks {
			if !yield(&model.LLMResponse{Content: &genai.Content{Parts: []*genai.Part{{Text: c}}}}, nil) {
				return
			}
		}
		yield(nil, errors.New("connection reset"))
	}
}

// collect drains m, returning text chunks and the terminal exhausted
// marker response, if any.
func collect(m model.LLM) (texts []string, lastResp *model.LLMResponse) {
	for resp, err := range m.GenerateContent(context.Background(), &model.LLMRequest{}, false) {
		if err != nil {
			continue
		}
		if resp.ErrorCode != "" {
			lastResp = resp
			continue
		}
		if resp.Content != nil && len(resp.Content.Parts) > 0 {
			texts = append(texts, resp.Content.Parts[0].Text)
		}
	}
	return texts, lastResp
}

func TestRetryModelRestartsCleanFailure(t *testing.T) {
	// Nothing yielded before the error: the request is retried and the
	// successful attempt's chunks are the only content delivered.
	inner := &flakyInner{chunks: []string{"a", "b"}, fails: 1}
	m := &retryModel{inner: inner, maxRetries: 2, delay: 0}
	texts, _ := collect(m)
	if inner.calls != 2 {
		t.Errorf("inner called %d times, want 2 (one failure + one retry)", inner.calls)
	}
	if got, want := strings.Join(texts, ""), "ab"; got != want {
		t.Errorf("texts = %q, want %q", got, want)
	}
}

func TestRetryModelGivesUpAfterMaxRetries(t *testing.T) {
	inner := &flakyInner{chunks: []string{"a"}, fails: 10}
	m := &retryModel{inner: inner, maxRetries: 2, delay: 0}
	texts, last := collect(m)
	if inner.calls != 3 { // initial attempt + 2 retries
		t.Errorf("inner called %d times, want 3", inner.calls)
	}
	if len(texts) != 0 {
		t.Errorf("texts = %v, want none (every attempt failed)", texts)
	}
	if last == nil || last.ErrorCode != "transient_error_exhausted" {
		t.Errorf("final response = %+v, want transient_error_exhausted marker", last)
	}
}

func TestRetryModelDoesNotReplayPartialStream(t *testing.T) {
	// Chunks delivered before the error must not be replayed by a retry.
	inner := &failingAfterChunks{chunks: []string{"partial-"}}
	m := &retryModel{inner: inner, maxRetries: 3, delay: 0}
	texts, last := collect(m)
	if inner.calls != 1 {
		t.Errorf("inner called %d times, want 1 (no restart after partial delivery)", inner.calls)
	}
	if got, want := strings.Join(texts, ""), "partial-"; got != want {
		t.Errorf("texts = %q, want exactly %q (no duplicated chunks)", got, want)
	}
	if last == nil || last.ErrorCode != "transient_error_exhausted" {
		t.Errorf("final response = %+v, want transient_error_exhausted marker", last)
	}
}

func TestRetryModelNonTransientFailsImmediately(t *testing.T) {
	inner := &failingAfterChunks{chunks: []string{"x"}}
	failing := &nonTransient{inner: inner}
	m := &retryModel{inner: failing, maxRetries: 3, delay: 0}
	if _, last := collect(m); last == nil || last.ErrorMessage == "" {
		t.Errorf("expected exhausted marker for non-transient error, got %+v", last)
	}
	if inner.calls != 1 {
		t.Errorf("inner called %d times, want 1", inner.calls)
	}
}

type nonTransient struct{ inner *failingAfterChunks }

func (n *nonTransient) Name() string { return n.inner.Name() }

func (n *nonTransient) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		for resp, err := range n.inner.GenerateContent(ctx, req, stream) {
			if err != nil {
				yield(nil, errors.New("bad request"))
				return
			}
			if !yield(resp, nil) {
				return
			}
		}
	}
}
