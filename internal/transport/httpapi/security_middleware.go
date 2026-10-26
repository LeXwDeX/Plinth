package httpapi

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/authentication"
)

// securityHeadersMiddleware 全站安全响应头（SEC-04）：头名与值全部来自
// Product Schema http.security_headers，代码不写死策略。
func (s *Server) securityHeadersMiddleware(next http.Handler) http.Handler {
	headers := s.application.Runtime.HTTP.SecurityHeaders
	return http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		for name, value := range headers {
			writer.Header().Set(name, value)
		}
		next.ServeHTTP(writer, request)
	})
}

// sameOriginCSRFMiddleware 状态变更请求的同站校验（SEC-03）。携带会话
// Cookie 的写请求必须提供可验证的浏览器来源头；机器调用无 Cookie 时
// 仍由对应路由的显式凭证、签名或身份边界处理。
// SameSite=Lax 只挡跨站，不挡同机其他端口/兄弟子域等"同站跨源"场景。
func (s *Server) sameOriginCSRFMiddleware(next http.Handler) http.Handler {
	enforced := s.application.Runtime.HTTP.CSRFOriginEnforced
	return http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if enforced {
			switch request.Method {
			case http.MethodPost, http.MethodPut, http.MethodPatch,
				http.MethodDelete:
				origin := request.Header.Get("Origin")
				fetchSite := request.Header.Get("Sec-Fetch-Site")
				if origin == "" && fetchSite == "" && request.Header.Get("Cookie") != "" {
					s.writeMessageProblem(writer, http.StatusForbidden, "forbidden", "error.forbidden")
					return
				}
				if origin != "" && !sameHost(request, origin) {
					s.writeMessageProblem(
						writer,
						http.StatusForbidden,
						"forbidden",
						"error.forbidden",
					)
					return
				}
				switch fetchSite {
				case "", "same-origin", "none":
				default:
					s.writeMessageProblem(
						writer,
						http.StatusForbidden,
						"forbidden",
						"error.forbidden",
					)
					return
				}
			}
		}
		next.ServeHTTP(writer, request)
	})
}

func sameHost(request *http.Request, origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Host, request.Host)
}

// loginFailureLimiter 按 IP+用户名 维度的滑动窗口失败限流（SEC-01）。
// enforceLoginRateLimit 在凭证校验前调用：已被限流返回 true（调用方
// 直接 429）。
func (s *Server) enforceLoginRateLimit(
	request *http.Request,
	username string,
) bool {
	return s.loginLimiter.blocked(
		request.Context(),
		loginClientKey(request, username),
	)
}

// noteLoginFailure 在凭证校验失败后调用。
func (s *Server) noteLoginFailure(
	request *http.Request,
	username string,
) {
	s.loginLimiter.record(request.Context(), loginClientKey(request, username))
}

var _ = authentication.ErrInvalidCredentials
