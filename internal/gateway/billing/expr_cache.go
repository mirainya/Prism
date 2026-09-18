package billing

import (
	"container/list"
	"sync"
)

const expressionCacheSize = 1024

// ExpressionCache memoises parsed expressions so a hot rate is not re-parsed on
// every call. The whitelist participates in the key: the same source text under
// a different manifest is a different expression, and caching it under one key
// would let one adapter's variables leak into another's validation.
type ExpressionCache struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	order   *list.List
}

type cacheEntry struct {
	key        string
	expression *Expression
}

func NewExpressionCache() *ExpressionCache {
	return &ExpressionCache{entries: make(map[string]*list.Element), order: list.New()}
}

// Parse returns the cached expression for the source, parsing it on a miss.
// Failures are not cached; a rejected expression is cheap and never reaches a
// hot path.
func (c *ExpressionCache) Parse(source string, declared map[string]bool, digest string) (*Expression, error) {
	if c == nil || digest == "" {
		return ParseExpression(source, declared)
	}
	key := digest + "\x00" + source
	c.mu.Lock()
	if element, ok := c.entries[key]; ok {
		c.order.MoveToFront(element)
		expression := element.Value.(*cacheEntry).expression
		c.mu.Unlock()
		return expression, nil
	}
	c.mu.Unlock()

	expression, err := ParseExpression(source, declared)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.entries[key]; ok {
		c.order.MoveToFront(element)
		return element.Value.(*cacheEntry).expression, nil
	}
	c.entries[key] = c.order.PushFront(&cacheEntry{key: key, expression: expression})
	for c.order.Len() > expressionCacheSize {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.order.Remove(oldest)
		delete(c.entries, oldest.Value.(*cacheEntry).key)
	}
	return expression, nil
}

func (c *ExpressionCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}
