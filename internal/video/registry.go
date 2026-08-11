package video

import "sync"

// AdapterFactory 创建 Adapter 实例
type AdapterFactory func(channel *VideoChannel, key *VideoChannelKey) Adapter

// Registry Adapter 注册表
type Registry struct {
	mu        sync.RWMutex
	factories map[string]AdapterFactory
}

func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]AdapterFactory)}
}

func (r *Registry) Register(adapterType string, f AdapterFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[adapterType] = f
}

func (r *Registry) Get(adapterType string, ch *VideoChannel, key *VideoChannelKey) Adapter {
	r.mu.RLock()
	f, ok := r.factories[adapterType]
	r.mu.RUnlock()
	if !ok {
		return nil
	}
	return f(ch, key)
}
