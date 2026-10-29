package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/authentication"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func (s *Server) legacyLogin(
	writer http.ResponseWriter,
	request *http.Request,
) {
	api := s.application.Authentication.API
	var body map[string]any
	if err := decodeAuthenticationBody(
		request,
		&body,
		authenticationBodyLimit(s.application),
	); err != nil {
		s.writeAuthenticationError(
			writer,
			http.StatusInternalServerError,
			s.application.Authentication.Messages.LoginFailed,
		)
		return
	}
	username, usernameOK := body[api.UsernameField].(string)
	password, passwordOK := body[api.PasswordField].(string)
	if !usernameOK || !passwordOK || username == "" || password == "" {
		s.writeAuthenticationError(
			writer,
			http.StatusBadRequest,
			s.application.Authentication.Messages.MissingCredentials,
		)
		return
	}
	// SEC-01：登录失败限流（IP+用户名滑动窗口，阈值来自 Schema）。
	if s.enforceLoginRateLimit(request, username) {
		s.writeAuthenticationError(
			writer,
			http.StatusTooManyRequests,
			s.application.Authentication.Messages.RateLimited,
		)
		return
	}
	user, err := s.authentication.AuthenticatePassword(
		request.Context(),
		username,
		password,
	)
	if err == nil {
		err = s.authentication.IssueSession(writer, request, user)
	}
	if err != nil {
		// 身份不完整（账号存在但缺 ID/名称/邮箱）与口令错误同属凭据问题：
		// 返 401 且用同一条文案，避免泄露账号是否存在；同时计入限流。
		if errors.Is(err, authentication.ErrInvalidCredentials) ||
			errors.Is(err, authentication.ErrUnauthenticated) {
			s.noteLoginFailure(request, username)
			if errors.Is(err, authentication.ErrUnauthenticated) {
				s.logger.Warn(
					"login rejected: account identity incomplete",
					"username", username,
					"error", err,
				)
			}
			s.writeAuthenticationError(
				writer,
				http.StatusUnauthorized,
				s.application.Authentication.Messages.InvalidCredentials,
			)
			return
		}
		// 真故障：必须落日志，否则「登录失败」既不可观测也不可归因。
		s.logger.Error("login failed", "username", username, "error", err)
		s.writeAuthenticationError(
			writer,
			http.StatusInternalServerError,
			s.application.Authentication.Messages.LoginFailed,
		)
		return
	}
	s.recordProductAudit(request, administration.AuditEvent{
		Action:       api.LoginAuditAction,
		ActorID:      user.ID,
		ActorName:    username,
		ResourceType: api.AuditResourceType,
		ResourceID:   user.ID,
	})
	s.clearSiteDataOnSessionChange(writer, request)
	writeJSON(writer, http.StatusOK, map[string]any{
		api.SuccessField: true,
		api.UserField:    s.authenticationUserWire(user),
	})
}

func (s *Server) legacySession(
	writer http.ResponseWriter,
	request *http.Request,
) {
	api := s.application.Authentication.API
	user, exists, err := s.authentication.CurrentUser(
		request.Context(),
		request,
	)
	if err != nil {
		s.writeAuthenticationError(
			writer,
			http.StatusInternalServerError,
			s.application.Authentication.Messages.InternalError,
		)
		return
	}
	var projectedUser any
	var admin bool
	if exists {
		projectedUser = s.authenticationUserWire(user)
		admin, err = s.authentication.IsAdmin(request.Context(), user)
		if err != nil {
			s.writeAuthenticationError(
				writer,
				http.StatusInternalServerError,
				s.application.Authentication.Messages.InternalError,
			)
			return
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		api.UserField:    projectedUser,
		api.IsAdminField: admin,
	})
}

func (s *Server) legacyLogout(
	writer http.ResponseWriter,
	request *http.Request,
) {
	api := s.application.Authentication.API
	user, exists, err := s.authentication.CurrentUser(
		request.Context(),
		request,
	)
	if err != nil {
		s.writeAuthenticationError(
			writer,
			http.StatusInternalServerError,
			s.application.Authentication.Messages.InternalError,
		)
		return
	}
	if exists {
		s.recordProductAudit(request, administration.AuditEvent{
			Action:       api.LogoutAuditAction,
			ActorID:      user.ID,
			ActorName:    user.Name,
			ResourceType: api.AuditResourceType,
			ResourceID:   user.ID,
		})
	}
	if err := s.authentication.RevokeSession(request.Context(), request); err != nil {
		s.writeAuthenticationError(writer, http.StatusInternalServerError, s.application.Authentication.Messages.InternalError)
		return
	}
	s.authentication.ClearSession(writer)
	s.clearSiteDataOnSessionChange(writer, request)
	writeJSON(writer, http.StatusOK, map[string]any{
		api.SuccessField: true,
	})
}

// clearSiteDataOnSessionChange asks the browser to drop cached private bytes
// (thumbnails) when the signed-in user changes (#225). Browsers only act on
// Clear-Site-Data in a secure context and log an error elsewhere, so a plain-HTTP
// deployment reached by IP address gets no header (#134); the front end clears its
// own per-account state instead, and thumbnail responses carry Vary: Cookie.
// Browsers that ignore the header still revalidate after the Schema's short
// thumbnail max-age.
func (s *Server) clearSiteDataOnSessionChange(writer http.ResponseWriter, request *http.Request) {
	value := s.application.Runtime.HTTP.SessionChangeClearSiteData
	if value != "" && requestIsSecureContext(request, s.trustedProxies) {
		writer.Header().Set("Clear-Site-Data", value)
	}
}

func (s *Server) legacyMockAuth(
	writer http.ResponseWriter,
	request *http.Request,
) {
	api := s.application.Authentication.API
	var body map[string]any
	_ = decodeAuthenticationBody(
		request,
		&body,
		authenticationBodyLimit(s.application),
	)
	name, _ := body[api.NameField].(string)
	email, _ := body[api.EmailField].(string)
	user, err := s.authentication.MockUser(name, email)
	switch {
	case errors.Is(err, authentication.ErrMockDisabled):
		s.writeAuthenticationError(
			writer,
			http.StatusForbidden,
			s.application.Authentication.Messages.MockDisabled,
		)
		return
	case errors.Is(err, authentication.ErrMockUserMissing):
		s.writeAuthenticationError(
			writer,
			http.StatusBadRequest,
			s.application.Authentication.Messages.MockUserMissing,
		)
		return
	case errors.Is(err, authentication.ErrMockUserInvalid):
		s.writeAuthenticationError(
			writer,
			http.StatusInternalServerError,
			s.application.Authentication.Messages.MockUserInvalid,
		)
		return
	case err != nil:
		s.writeAuthenticationError(
			writer,
			http.StatusInternalServerError,
			s.application.Authentication.Messages.InternalError,
		)
		return
	}
	if err := s.authentication.IssueSession(writer, request, user); err != nil {
		s.writeAuthenticationError(
			writer,
			http.StatusInternalServerError,
			s.application.Authentication.Messages.InternalError,
		)
		return
	}
	// #225: a login is a session change; drop cached private bytes.
	s.clearSiteDataOnSessionChange(writer, request)
	writeJSON(writer, http.StatusOK, map[string]any{
		api.UserField: s.authenticationUserWire(user),
	})
}

func (s *Server) authenticationUserWire(
	user authentication.User,
) map[string]any {
	api := s.application.Authentication.API
	return map[string]any{
		api.IDField:    user.ID,
		api.NameField:  user.Name,
		api.EmailField: user.Email,
	}
}

func (s *Server) writeAuthenticationError(
	writer http.ResponseWriter,
	status int,
	message string,
) {
	writeJSON(writer, status, map[string]string{
		s.application.Authentication.API.ErrorField: traceFromWriter(writer).localeText(message),
	})
}

func decodeAuthenticationBody(
	request *http.Request,
	target any,
	maxBytes int64,
) error {
	decoder := json.NewDecoder(io.LimitReader(request.Body, maxBytes))
	return decoder.Decode(target)
}

// authenticationBodyLimit 返回鉴权端点的请求体上限（SEC-05）：鉴权端点
// 未认证即可达，不与画布等大体积端点共用全局上限。
func authenticationBodyLimit(application catalog.ApplicationSchema) int64 {
	limit := application.Authentication.API.AuthJSONBodyMaxBytes
	if limit > 0 {
		return limit
	}
	return application.Runtime.HTTP.JSONBodyMaxBytes
}

// changeOwnPassword 是自助改密端点：已认证用户改自己的密码，必须先验当前
// 密码。改密成功后该用户的全部会话被吊销，因此当前请求结束时自己的会话
// 也已失效——前端据此回到登录页。
func (s *Server) changeOwnPassword(
	writer http.ResponseWriter,
	request *http.Request,
) {
	api := s.application.Authentication.API
	messages := s.application.Authentication.Messages
	// Current-password guesses bind to the authenticated account ID, never to
	// its mutable display name or a caller-supplied identity.
	principal, err := s.identity.Identify(request)
	if err != nil {
		s.writeAuthenticationError(
			writer,
			http.StatusUnauthorized,
			messages.Unauthenticated,
		)
		return
	}
	userID := principal.ID()
	var body map[string]any
	if err := decodeAuthenticationBody(
		request,
		&body,
		authenticationBodyLimit(s.application),
	); err != nil {
		s.writeAuthenticationError(
			writer,
			http.StatusBadRequest,
			messages.MissingCredentials,
		)
		return
	}
	current, currentOK := body[api.CurrentPasswordField].(string)
	next, nextOK := body[api.NewPasswordField].(string)
	if !currentOK || !nextOK || current == "" || next == "" {
		s.writeAuthenticationError(
			writer,
			http.StatusBadRequest,
			messages.MissingCredentials,
		)
		return
	}
	failureKey := passwordChangeClientKey(request, userID)
	if s.loginLimiter.blocked(request.Context(), failureKey) {
		s.writeAuthenticationError(writer, http.StatusTooManyRequests, messages.RateLimited)
		return
	}
	if err := s.authentication.ChangeOwnPassword(
		request.Context(),
		userID,
		current,
		next,
	); err != nil {
		switch {
		case errors.Is(err, authentication.ErrInvalidCredentials):
			// An incorrect current password consumes this account's guess budget.
			s.loginLimiter.record(request.Context(), failureKey)
			s.writeAuthenticationError(
				writer,
				http.StatusUnauthorized,
				messages.InvalidCurrentPassword,
			)
		case errors.Is(err, authentication.ErrWeakPassword):
			s.writeAuthenticationError(
				writer,
				http.StatusBadRequest,
				strings.ReplaceAll(
					messages.WeakPassword,
					"{minimum}",
					strconv.Itoa(api.PasswordMinimumCharacters),
				),
			)
		case errors.Is(err, authentication.ErrUnauthenticated):
			s.writeAuthenticationError(
				writer,
				http.StatusUnauthorized,
				messages.Unauthenticated,
			)
		default:
			s.logger.Error(
				"change own password failed",
				"user_id", userID,
				"error", err,
			)
			s.writeAuthenticationError(
				writer,
				http.StatusInternalServerError,
				messages.InternalError,
			)
		}
		return
	}
	s.recordProductAudit(request, administration.AuditEvent{
		Action:       api.PasswordChangeAuditAction,
		ActorID:      userID,
		ActorName:    principal.Claims.Name,
		ResourceType: api.AuditResourceType,
		ResourceID:   userID,
	})
	s.authentication.ClearSession(writer)
	writeJSON(writer, http.StatusOK, map[string]any{
		api.SuccessField: true,
		api.ErrorField:   messages.PasswordChanged,
	})
}
