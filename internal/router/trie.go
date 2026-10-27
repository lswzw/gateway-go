package router

import (
	"sort"
	"strings"
	"sync"
)

// TrieNode Trie 树节点
type TrieNode struct {
	children map[string]*TrieNode
	routes   []*RouteDefinition // 在该路径节点上注册的所有路由
	route    *RouteDefinition   // 保持向后兼容：最高优先级路由
	isEnd    bool
}

// TrieRouter 基于前缀树的路径路由器 (并发安全，支持前缀回溯)
type TrieRouter struct {
	root *TrieNode
	mu   sync.RWMutex
}

// NewTrieRouter 创建 Trie 路由器实例
func NewTrieRouter() *TrieRouter {
	return &TrieRouter{
		root: &TrieNode{
			children: make(map[string]*TrieNode),
		},
	}
}

// splitPath 将 URL 路径拆分为路径段切片，自动去除前后斜杠与连续空段
func splitPath(path string) []string {
	clean := strings.Trim(path, "/")
	if clean == "" {
		return nil
	}
	parts := strings.Split(clean, "/")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// sortRoutes 排序路由：优先级高在前，同优先级下精确匹配优先于前缀匹配
func sortRoutes(routes []*RouteDefinition) {
	sort.SliceStable(routes, func(i, j int) bool {
		if routes[i].Match.Priority != routes[j].Match.Priority {
			return routes[i].Match.Priority > routes[j].Match.Priority
		}
		// 同优先级下 exact 优先于 prefix
		iExact := routes[i].Match.Type == MatchExact || string(routes[i].Match.Type) == "exact"
		jExact := routes[j].Match.Type == MatchExact || string(routes[j].Match.Type) == "exact"
		if iExact && !jExact {
			return true
		}
		return false
	})
}

// Insert 插入路由规则
func (tr *TrieRouter) Insert(path string, route *RouteDefinition) {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	parts := splitPath(path)
	node := tr.root

	for _, part := range parts {
		if node.children == nil {
			node.children = make(map[string]*TrieNode)
		}

		child, exists := node.children[part]
		if !exists {
			child = &TrieNode{
				children: make(map[string]*TrieNode),
			}
			node.children[part] = child
		}
		node = child
	}

	node.isEnd = true
	node.routes = append(node.routes, route)
	sortRoutes(node.routes)
	node.route = node.routes[0]
}

// FindCandidateRoutes 查找给定路径的所有候选路由 (支持精确末节点 + 父级前缀回溯) (REQ-P1-03)
// 候选路由按路径特异度排序：精确节点路由在前，随后沿树自底向上回溯父级 prefix 路由
func (tr *TrieRouter) FindCandidateRoutes(path string) []*RouteDefinition {
	tr.mu.RLock()
	defer tr.mu.RUnlock()

	parts := splitPath(path)
	node := tr.root

	// visited 存储自根向下的所有路径节点: visited[0] 是 root, visited[len(parts)] 是末节点
	visited := make([]*TrieNode, 0, len(parts)+1)
	visited = append(visited, node)

	for _, part := range parts {
		if node.children == nil {
			break
		}
		child, exists := node.children[part]
		if !exists {
			break
		}
		node = child
		visited = append(visited, node)
	}

	var candidates []*RouteDefinition
	matchedDepth := len(visited) - 1

	// 1. 若完全匹配了所有 parts (即存在末节点)：优先采纳末节点上的所有路由
	if matchedDepth == len(parts) {
		leaf := visited[matchedDepth]
		if leaf.isEnd && len(leaf.routes) > 0 {
			candidates = append(candidates, leaf.routes...)
		}
	}

	// 2. 自底向上回溯父级节点 (Prefix Fallback)，寻找声明为 prefix 的父路由
	for i := matchedDepth; i >= 0; i-- {
		// 若当前节点即为完全匹配的 leaf 节点，前面已添加，跳过避免重复
		if i == len(parts) {
			continue
		}

		ancestor := visited[i]
		if !ancestor.isEnd || len(ancestor.routes) == 0 {
			continue
		}

		// 对于祖先节点，仅具有 prefix 匹配语义的路由才有效
		for _, r := range ancestor.routes {
			if r.Match.Type == MatchPrefix || string(r.Match.Type) == "prefix" {
				candidates = append(candidates, r)
			}
		}
	}

	return candidates
}

// Match 匹配路由：返回最匹配的路由定义；若无完全匹配节点，则自动回退至父级前缀节点 (REQ-P1-03)
func (tr *TrieRouter) Match(path string) (*RouteDefinition, bool) {
	candidates := tr.FindCandidateRoutes(path)
	if len(candidates) > 0 {
		return candidates[0], true
	}
	return nil, false
}
