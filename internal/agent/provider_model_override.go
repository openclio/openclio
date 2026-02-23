package agent

import "context"

type providerWithModel struct {
	provider    Provider
	model       string
	onlyIfEmpty bool
}

func (p *providerWithModel) Name() string { return p.provider.Name() }

func (p *providerWithModel) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if p.model != "" && (!p.onlyIfEmpty || req.Model == "") {
		req.Model = p.model
	}
	return p.provider.Chat(ctx, req)
}

type streamerWithModel struct {
	*providerWithModel
	streamer Streamer
}

func (s *streamerWithModel) Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	if s.model != "" && (!s.onlyIfEmpty || req.Model == "") {
		req.Model = s.model
	}
	return s.streamer.Stream(ctx, req)
}

// WithModel forces a provider to use a specific model name at call time.
// Streaming support is preserved when the wrapped provider implements Streamer.
func WithModel(p Provider, model string) Provider {
	if p == nil || model == "" {
		return p
	}
	base := &providerWithModel{provider: p, model: model}
	if streamer, ok := p.(Streamer); ok {
		return &streamerWithModel{
			providerWithModel: base,
			streamer:          streamer,
		}
	}
	return base
}

// WithDefaultModel sets a model only when the request model is empty.
func WithDefaultModel(p Provider, model string) Provider {
	if p == nil || model == "" {
		return p
	}
	base := &providerWithModel{provider: p, model: model, onlyIfEmpty: true}
	if streamer, ok := p.(Streamer); ok {
		return &streamerWithModel{
			providerWithModel: base,
			streamer:          streamer,
		}
	}
	return base
}

type providerWithRouter struct {
	provider Provider
	router   *ModelRouter
}

func (p *providerWithRouter) Name() string { return p.provider.Name() }

func (p *providerWithRouter) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if p.router != nil {
		route := p.router.Select(req)
		if route.Model != "" {
			req.Model = route.Model
		}
		if p.router.logger != nil {
			p.router.logger.Debug("model router selected model",
				"provider", p.provider.Name(),
				"tier", string(route.Tier),
				"model", route.Model,
				"reasons", route.Reasons,
			)
		}
	}
	return p.provider.Chat(ctx, req)
}

type streamerWithRouter struct {
	*providerWithRouter
	streamer Streamer
}

func (s *streamerWithRouter) Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	if s.router != nil {
		route := s.router.Select(req)
		if route.Model != "" {
			req.Model = route.Model
		}
		if s.router.logger != nil {
			s.router.logger.Debug("model router selected model (stream)",
				"provider", s.provider.Name(),
				"tier", string(route.Tier),
				"model", route.Model,
				"reasons", route.Reasons,
			)
		}
	}
	return s.streamer.Stream(ctx, req)
}

// WithModelRouter applies heuristic model routing before provider calls.
func WithModelRouter(p Provider, router *ModelRouter) Provider {
	if p == nil || router == nil {
		return p
	}
	base := &providerWithRouter{provider: p, router: router}
	if streamer, ok := p.(Streamer); ok {
		return &streamerWithRouter{
			providerWithRouter: base,
			streamer:           streamer,
		}
	}
	return base
}
