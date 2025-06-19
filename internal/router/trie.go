package router

import (
	"strings"
	"sync"
)

// TrieNode Trie树节点
type TrieNode struct {
	children map[string]*TrieNode
	route    *RouteDefinition
	isEnd    bool
}

// TrieRouter 基于Trie树的路由器
type TrieRouter struct {
	root *TrieNode
	mu   sync.RWMutex
}

// NewTrieRouter 创建Trie路由器
func NewTrieRouter() *TrieRouter {
	return &TrieRouter{
		root: &TrieNode{
			children: make(map[string]*TrieNode),
		},
	}
}

// Insert 插入路由
func (tr *TrieRouter) Insert(path string, route *RouteDefinition) {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	parts := strings.Split(strings.Trim(path, "/"), "/")
	node := tr.root

	for _, part := range parts {
		if node.children == nil {
			node.children = make(map[string]*TrieNode)
		}

		if _, exists := node.children[part]; !exists {
			node.children[part] = &TrieNode{
				children: make(map[string]*TrieNode),
			}
		}
		node = node.children[part]
	}

	node.isEnd = true
	node.route = route
}

// Match 匹配路由
func (tr *TrieRouter) Match(path string) (*RouteDefinition, bool) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()

	parts := strings.Split(strings.Trim(path, "/"), "/")
	node := tr.root

	for _, part := range parts {
		if node.children == nil {
			return nil, false
		}

		if child, exists := node.children[part]; exists {
			node = child
		} else {
			return nil, false
		}
	}

	if node.isEnd {
		return node.route, true
	}

	return nil, false
}
