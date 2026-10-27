package config

import (
	"fmt"
	"path"
	"strings"
)

// GlobToRegex 将 glob 通配符路径模式转换为正则表达式
// 支持:
// - `**`: 匹配任意字符序列（包括路径分隔符 /）
// - `*`: 匹配单个路径段内的任意字符（不跨越 /，即 [^/]*）
// - `?`: 匹配单个非 / 字符
// - 正确转义其他正则元字符 (., +, (, ), [, ], {, }, ^, $, |, \)
func GlobToRegex(pattern string) string {
	var b strings.Builder
	b.WriteString("^")

	i := 0
	n := len(pattern)
	for i < n {
		if i+1 < n && pattern[i] == '*' && pattern[i+1] == '*' {
			// `**` 通配符
			b.WriteString(".*")
			i += 2
			continue
		}
		if pattern[i] == '*' {
			// 单个 `*` 通配符：只匹配单个路径段
			b.WriteString("[^/]*")
			i++
			continue
		}
		if pattern[i] == '?' {
			// `?` 通配符：匹配单个非 / 字符
			b.WriteString("[^/]")
			i++
			continue
		}
		// 检查并转义正则表达式特殊字符
		c := pattern[i]
		switch c {
		case '.', '+', '(', ')', '[', ']', '{', '}', '^', '$', '|', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
		i++
	}

	b.WriteString("$")
	return b.String()
}

// ShadowWarning 记录路由遮蔽告警信息 (REQ-P1-03)
type ShadowWarning struct {
	ShadowRoute   string `json:"shadow_route"`   // 产生遮蔽的更泛化或同级高优先级路由名称
	ShadowedRoute string `json:"shadowed_route"` // 被完全遮蔽、无法被访问的路由名称
	Reason        string `json:"reason"`         // 遮蔽原因说明
}

// DetectShadowedRoutes 检测路由列表中存在的路由遮蔽现象
// 在启动期和 -t 配置语法测试时调用并输出告警日志
func DetectShadowedRoutes(routes []RouteConfig) []ShadowWarning {
	var warnings []ShadowWarning
	seenPairs := make(map[string]bool)

	for i := 0; i < len(routes); i++ {
		a := &routes[i]
		for j := 0; j < len(routes); j++ {
			if i == j {
				continue
			}
			b := &routes[j]

			pairKey := fmt.Sprintf("%s->%s", a.Name, b.Name)
			if seenPairs[pairKey] {
				continue
			}

			if isShadowed, reason := checkRouteShadowing(a, b, i, j); isShadowed {
				seenPairs[pairKey] = true
				warnings = append(warnings, ShadowWarning{
					ShadowRoute:   a.Name,
					ShadowedRoute: b.Name,
					Reason:        reason,
				})
			}
		}
	}

	return warnings
}

// checkRouteShadowing 检查路由 a 是否完全或潜在遮蔽路由 b
func checkRouteShadowing(a, b *RouteConfig, indexA, indexB int) (bool, string) {
	// 1. Method 条件覆盖检查：a 的匹配方法必须覆盖 b 的所有方法
	if !methodCovers(a.Match.Method, b.Match.Method) {
		return false, ""
	}

	// 2. Headers 条件覆盖检查：a 要求的 Header 必须是 b 的子集
	if !headersCover(a.Match.Headers, b.Match.Headers) {
		return false, ""
	}

	// 3. QueryParams 条件覆盖检查：a 要求的 Query 参数必须是 b 的子集
	if !queryCovers(a.Match.QueryParams, b.Match.QueryParams) {
		return false, ""
	}

	// 4. Path 模式覆盖检查：a 的路径规则必须能够匹配 b 的所有请求路径
	if !pathCovers(a.Match, b.Match) {
		return false, ""
	}

	// 5. Host 与优先级裁决检查
	hostA := strings.ToLower(strings.TrimSpace(a.Match.Host))
	hostB := strings.ToLower(strings.TrimSpace(b.Match.Host))

	// 场景 A: 相同 Host 范围 (同为无 Host，或同为相同的具体/通配 Host)
	if hostA == hostB {
		// 如果 a 的优先级严格高于 b，则 a 彻底遮蔽 b
		if a.Match.Priority > b.Match.Priority {
			reason := fmt.Sprintf("泛化路由 [%s] (优先级 %d, 路径 %s) 优先级高于更具体的路由 [%s] (优先级 %d, 路径 %s)，后者将永远无法被访问",
				a.Name, a.Match.Priority, a.Match.Path, b.Name, b.Match.Priority, b.Match.Path)
			return true, reason
		}

		// 如果优先级相同且路径定义完全相同，且 a 在 b 之前定义
		if a.Match.Priority == b.Match.Priority && indexA < indexB && isPathPatternIdentical(a.Match, b.Match) {
			reason := fmt.Sprintf("路由 [%s] 与路由 [%s] 拥有相同的路径规则 (%s) 与优先级 (%d)，先定义的路由将完全遮蔽后续路由",
				a.Name, b.Name, a.Match.Path, a.Match.Priority)
			return true, reason
		}

		return false, ""
	}

	// 场景 B: a 无 Host (泛化匹配所有 Host)，而 b 指定了具体 Host
	// 虽然运行时按 Host 优先规则生效，但如果泛化路由优先级 >= 具体 Host 路由，
	// 易造成运维认知偏差并触发优先级倒置告警 (如 config.yaml 中的 /api prio 90 vs v1.api.example.com prio 80)
	if hostA == "" && hostB != "" {
		if a.Match.Priority >= b.Match.Priority {
			reason := fmt.Sprintf("泛化无 Host 路由 [%s] (优先级 %d, 路径 %s) 与具体 Host 路由 [%s] (Host: %s, 优先级 %d, 路径 %s) 路径重叠且优先级较高或相同，存在路由遮蔽或优先级倒置隐患",
				a.Name, a.Match.Priority, a.Match.Path, b.Name, b.Match.Host, b.Match.Priority, b.Match.Path)
			return true, reason
		}
	}

	return false, ""
}

// methodCovers 检查方法过滤条件是否覆盖
func methodCovers(methodA, methodB string) bool {
	if methodA == "" {
		// A 未限定方法，匹配所有方法
		return true
	}
	if methodB == "" {
		// B 匹配所有方法，而 A 仅匹配部分方法，不能完全遮蔽
		return false
	}

	setA := make(map[string]bool)
	for _, m := range strings.Split(methodA, ",") {
		m = strings.ToUpper(strings.TrimSpace(m))
		if m != "" {
			setA[m] = true
		}
	}

	for _, m := range strings.Split(methodB, ",") {
		m = strings.ToUpper(strings.TrimSpace(m))
		if m != "" && !setA[m] {
			return false
		}
	}

	return true
}

// headersCover 检查 Header 过滤条件是否覆盖
func headersCover(headersA, headersB map[string]string) bool {
	if len(headersA) == 0 {
		return true
	}
	if len(headersB) < len(headersA) {
		return false
	}
	for k, vA := range headersA {
		if vB, ok := headersB[k]; !ok || vA != vB {
			return false
		}
	}
	return true
}

// queryCovers 检查 Query 参数过滤条件是否覆盖
func queryCovers(queryA, queryB map[string]string) bool {
	if len(queryA) == 0 {
		return true
	}
	if len(queryB) < len(queryA) {
		return false
	}
	for k, vA := range queryA {
		if vB, ok := queryB[k]; !ok || vA != vB {
			return false
		}
	}
	return true
}

// pathCovers 检查路径模式 a 是否能匹配路径模式 b 的所有路径
func pathCovers(matchA, matchB RouteMatch) bool {
	typeA := matchA.Type
	if typeA == "" {
		typeA = "prefix"
	}
	typeB := matchB.Type
	if typeB == "" {
		typeB = "prefix"
	}

	pathA := matchA.Path
	pathB := matchB.Path

	// A 是根路径前缀 /，匹配所有可能路径
	if (typeA == "prefix" || typeA == "") && (pathA == "/" || pathA == "") {
		return true
	}

	// 相同路径且相同类型
	if pathA == pathB && typeA == typeB {
		return true
	}

	// A 是前缀匹配
	if typeA == "prefix" {
		cleanA := strings.TrimSuffix(pathA, "/")
		if pathB == cleanA {
			return true
		}
		if strings.HasPrefix(pathB, cleanA+"/") {
			return true
		}
	}

	// A 是通配符匹配
	if typeA == "wildcard" {
		if pathA == "/*" || pathA == "/**" {
			return true
		}
		// 通配符前缀如 /api/* 覆盖 /api/users
		if strings.HasSuffix(pathA, "/*") {
			baseA := strings.TrimSuffix(pathA, "/*")
			if strings.HasPrefix(pathB, baseA+"/") || pathB == baseA {
				return true
			}
		}
		if strings.HasSuffix(pathA, "/**") {
			baseA := strings.TrimSuffix(pathA, "/**")
			if strings.HasPrefix(pathB, baseA+"/") || pathB == baseA {
				return true
			}
		}
	}

	// A 是正则全匹配
	if typeA == "regex" {
		patternA := matchA.Regex
		if patternA == "" {
			patternA = matchA.Path
		}
		if patternA == ".*" || patternA == "^/.*" || patternA == "^.*$" {
			return true
		}
	}

	return false
}

// isPathPatternIdentical 检查两个路由的路径模式是否完全等同
func isPathPatternIdentical(matchA, matchB RouteMatch) bool {
	typeA := matchA.Type
	if typeA == "" {
		typeA = "prefix"
	}
	typeB := matchB.Type
	if typeB == "" {
		typeB = "prefix"
	}

	if typeA != typeB {
		return false
	}

	patternA := matchA.Path
	if typeA == "regex" && matchA.Regex != "" {
		patternA = matchA.Regex
	}

	patternB := matchB.Path
	if typeB == "regex" && matchB.Regex != "" {
		patternB = matchB.Regex
	}

	return path.Clean(patternA) == path.Clean(patternB)
}
