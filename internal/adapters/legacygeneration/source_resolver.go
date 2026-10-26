package legacygeneration

import (
	"errors"
	"net/url"
	"strings"
)

type SourceResolver struct {
	proxyPath      string
	targetURLQuery string
}

func NewSourceResolver(
	proxyPath string,
	targetURLQuery string,
) (*SourceResolver, error) {
	resolver := &SourceResolver{
		proxyPath:      strings.TrimSpace(proxyPath),
		targetURLQuery: strings.TrimSpace(targetURLQuery),
	}
	if resolver.proxyPath == "" || resolver.targetURLQuery == "" {
		return nil, errors.New(
			"历史资产代理路由或目标 URL 查询字段未配置",
		)
	}
	return resolver, nil
}

func (resolver *SourceResolver) Resolve(source string) string {
	parsed, err := url.Parse(strings.TrimSpace(source))
	if err != nil || parsed.Path != resolver.proxyPath {
		return source
	}
	target := strings.TrimSpace(parsed.Query().Get(resolver.targetURLQuery))
	if target == "" {
		return source
	}
	return target
}
