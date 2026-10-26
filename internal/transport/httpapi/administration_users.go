package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

type administrationUserHandler func(
	http.ResponseWriter,
	*http.Request,
	access.Principal,
)

func (s *Server) registerAdministrationUserContracts() {
	routes := s.application.Runtime.HTTP.LegacyRoutes
	s.mux.HandleFunc(
		"GET "+routes.AdminUsers,
		s.withAdministrationUserPrincipal(s.listAdministrationUsers),
	)
	s.mux.HandleFunc(
		"POST "+routes.AdminUsers,
		s.withAdministrationUserPrincipal(s.createAdministrationUser),
	)
	s.mux.HandleFunc(
		"PATCH "+routes.AdminUser,
		s.withAdministrationUserPrincipal(s.updateAdministrationUser),
	)
	s.mux.HandleFunc(
		"DELETE "+routes.AdminUser,
		s.withAdministrationUserPrincipal(s.deleteAdministrationUser),
	)
}

func (s *Server) withAdministrationUserPrincipal(
	next administrationUserHandler,
) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		principal, err := s.identity.Identify(request)
		if err != nil {
			s.writeAdministrationUserError(
				writer,
				http.StatusUnauthorized,
				s.application.Authentication.Messages.Unauthenticated,
			)
			return
		}
		permission := access.Permission(
			s.application.Administration.Users.Permission,
		)
		if !principal.Can(permission) {
			s.writeAdministrationUserError(
				writer,
				http.StatusForbidden,
				s.application.Authentication.Messages.Forbidden,
			)
			return
		}
		next(writer, request, principal)
	}
}

func (s *Server) listAdministrationUsers(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	users, err := s.administration.ListUsers(request.Context())
	if err != nil {
		s.logger.Error("list administration users", "error", err)
		s.writeAdministrationUserError(
			writer,
			http.StatusInternalServerError,
			s.application.Administration.Users.Messages.InternalError,
		)
		return
	}
	projected := make([]map[string]any, 0, len(users))
	for _, user := range users {
		projected = append(projected, s.administrationUserWire(user))
	}
	s.recordAdministrationReadAudit(
		request,
		principal,
		catalog.AdministrationReadAuditSchema{
			ReadAction:   s.application.Administration.Users.Audit.ReadAction,
			ResourceType: s.application.Administration.Users.Audit.ResourceType,
		},
	)
	s.writeAdministrationUserJSON(writer, http.StatusOK, map[string]any{
		s.application.Administration.Users.API.Fields.Data: projected,
	})
}

func (s *Server) createAdministrationUser(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	raw, err := s.decodeAdministrationUserObject(request)
	if err != nil {
		s.writeAdministrationUserError(
			writer,
			http.StatusBadRequest,
			s.application.Administration.Users.Messages.InvalidRequest,
		)
		return
	}
	fields := s.application.Administration.Users.API.Fields
	username, ok := administrationString(raw, fields.Username)
	if !ok {
		username = ""
	}
	password, ok := administrationString(raw, fields.Password)
	if !ok {
		password = ""
	}
	name, _ := administrationString(raw, fields.Name)
	email, _ := administrationString(raw, fields.Email)
	admin, valid := administrationBool(raw, fields.IsAdmin)
	if !valid {
		s.writeAdministrationUserError(
			writer,
			http.StatusBadRequest,
			s.application.Administration.Users.Messages.InvalidRequest,
		)
		return
	}
	user, err := s.administration.CreateUser(
		request.Context(),
		s.administrationActor(request, principal),
		administration.CreateUser{
			Username: username,
			Password: password,
			Name:     name,
			Email:    email,
			Admin:    admin,
		},
	)
	if err != nil {
		s.writeAdministrationUserDomainError(writer, err)
		return
	}
	s.writeAdministrationUserJSON(writer, http.StatusCreated, map[string]any{
		fields.Data: s.administrationUserWire(user),
	})
}

func (s *Server) updateAdministrationUser(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	raw, err := s.decodeAdministrationUserObject(request)
	if err != nil {
		s.writeAdministrationUserError(
			writer,
			http.StatusBadRequest,
			s.application.Administration.Users.Messages.InvalidRequest,
		)
		return
	}
	fields := s.application.Administration.Users.API.Fields
	command := administration.UpdateUser{}
	if value, exists, valid := administrationOptionalString(
		raw,
		fields.Name,
	); !valid {
		s.writeAdministrationUserError(
			writer,
			http.StatusBadRequest,
			s.application.Administration.Users.Messages.InvalidRequest,
		)
		return
	} else if exists {
		command.Name = &value
	}
	if value, exists, valid := administrationOptionalString(
		raw,
		fields.Email,
	); !valid {
		s.writeAdministrationUserError(
			writer,
			http.StatusBadRequest,
			s.application.Administration.Users.Messages.InvalidRequest,
		)
		return
	} else if exists {
		command.Email = &value
	}
	if value, exists, valid := administrationOptionalString(
		raw,
		fields.Password,
	); !valid {
		s.writeAdministrationUserError(
			writer,
			http.StatusBadRequest,
			s.application.Administration.Users.Messages.InvalidRequest,
		)
		return
	} else if exists {
		command.Password = &value
	}
	if value, exists, valid := administrationOptionalBool(
		raw,
		fields.IsAdmin,
	); !valid {
		s.writeAdministrationUserError(
			writer,
			http.StatusBadRequest,
			s.application.Administration.Users.Messages.InvalidRequest,
		)
		return
	} else if exists {
		command.Admin = &value
	}
	if value, exists, valid := administrationOptionalBool(
		raw,
		fields.IsActive,
	); !valid {
		s.writeAdministrationUserError(
			writer,
			http.StatusBadRequest,
			s.application.Administration.Users.Messages.InvalidRequest,
		)
		return
	} else if exists {
		command.Active = &value
	}
	id := request.PathValue(
		s.application.Administration.Users.API.PathParameters.UserID,
	)
	user, err := s.administration.UpdateUser(
		request.Context(),
		s.administrationActor(request, principal),
		id,
		command,
	)
	if err != nil {
		s.writeAdministrationUserDomainError(writer, err)
		return
	}
	// SEC-06：改密、停用或收回管理员后立即吊销该用户全部会话，防止
	// 攻击者手里的既有 Cookie 继续有效。
	if command.Password != nil || command.Active != nil || command.Admin != nil {
		if revokeErr := s.authentication.RevokeUserSessions(
			request.Context(),
			id,
		); revokeErr != nil {
			s.logger.Warn(
				"吊销用户会话失败",
				"user_id", id,
				"error", revokeErr,
			)
		}
	}
	s.writeAdministrationUserJSON(writer, http.StatusOK, map[string]any{
		fields.Data: s.administrationUserWire(user),
	})
}

func (s *Server) deleteAdministrationUser(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	users := s.application.Administration.Users
	id := request.PathValue(users.API.PathParameters.UserID)
	err := s.administration.DeleteUser(
		request.Context(),
		s.administrationActor(request, principal),
		id,
	)
	if err != nil {
		s.writeAdministrationUserDomainError(writer, err)
		return
	}
	s.writeAdministrationUserJSON(writer, http.StatusOK, map[string]any{
		users.API.Fields.Success: true,
	})
}

func (s *Server) administrationActor(
	request *http.Request,
	principal access.Principal,
) administration.Actor {
	headers := s.application.Administration.AuditLogs.RequestHeaders
	ipAddress := request.Header.Get(headers.ForwardedFor)
	if ipAddress == "" {
		ipAddress = request.Header.Get(headers.RealIP)
	}
	return administration.Actor{
		ID:        principal.ID(),
		Username:  principal.Claims.Name,
		IPAddress: strings.TrimSpace(ipAddress),
		UserAgent: request.Header.Get(headers.UserAgent),
	}
}

func (s *Server) administrationUserWire(
	user administration.User,
) map[string]any {
	fields := s.application.Administration.Users.API.Fields
	var lastLogin any
	if user.LastLogin != nil {
		lastLogin = user.LastLogin.UTC().Format(time.RFC3339Nano)
	}
	var openID any
	if user.OpenID != nil {
		openID = *user.OpenID
	}
	return map[string]any{
		fields.ID:        user.ID,
		fields.Username:  user.Username,
		fields.Nickname:  user.Nickname,
		fields.Name:      user.Name,
		fields.Email:     user.Email,
		fields.IsAdmin:   user.Admin,
		fields.IsActive:  user.Active,
		fields.CreatedAt: user.CreatedAt.UTC().Format(time.RFC3339Nano),
		fields.LastLogin: lastLogin,
		fields.OpenID:    openID,
	}
}

func (s *Server) decodeAdministrationUserObject(
	request *http.Request,
) (map[string]json.RawMessage, error) {
	maxBytes := s.application.Runtime.HTTP.JSONBodyMaxBytes
	encoded, err := io.ReadAll(io.LimitReader(request.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(encoded)) > maxBytes {
		return nil, errors.New("administration: request too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	var raw map[string]json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, errors.New("administration: request must be object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("administration: trailing JSON")
		}
		return nil, err
	}
	return raw, nil
}

func administrationString(
	raw map[string]json.RawMessage,
	field string,
) (string, bool) {
	value, exists, valid := administrationOptionalString(raw, field)
	return value, exists && valid
}

func administrationOptionalString(
	raw map[string]json.RawMessage,
	field string,
) (string, bool, bool) {
	encoded, exists := raw[field]
	if !exists {
		return "", false, true
	}
	var value string
	if err := json.Unmarshal(encoded, &value); err != nil {
		return "", true, false
	}
	return value, true, true
}

func administrationBool(
	raw map[string]json.RawMessage,
	field string,
) (bool, bool) {
	value, exists, valid := administrationOptionalBool(raw, field)
	if !exists {
		return false, true
	}
	return value, valid
}

func administrationOptionalBool(
	raw map[string]json.RawMessage,
	field string,
) (bool, bool, bool) {
	encoded, exists := raw[field]
	if !exists {
		return false, false, true
	}
	var value bool
	if err := json.Unmarshal(encoded, &value); err != nil {
		return false, true, false
	}
	return value, true, true
}

func (s *Server) writeAdministrationUserDomainError(
	writer http.ResponseWriter,
	err error,
) {
	noteCause(writer, err)
	users := s.application.Administration.Users
	switch {
	case errors.Is(err, administration.ErrInvalidUsername):
		s.writeAdministrationUserError(
			writer,
			http.StatusBadRequest,
			administrationMessage(
				users.Messages.InvalidUsername,
				users.Limits.UsernameMinimumCharacters,
			),
		)
	case errors.Is(err, administration.ErrInvalidPassword):
		s.writeAdministrationUserError(
			writer,
			http.StatusBadRequest,
			administrationMessage(
				users.Messages.InvalidPassword,
				users.Limits.PasswordMinimumCharacters,
			),
		)
	case errors.Is(err, administration.ErrInvalidEmail):
		s.writeAdministrationUserError(
			writer,
			http.StatusBadRequest,
			users.Messages.InvalidEmail,
		)
	case errors.Is(err, administration.ErrDuplicateUsername):
		s.writeAdministrationUserError(
			writer,
			http.StatusConflict,
			users.Messages.DuplicateUsername,
		)
	case errors.Is(err, administration.ErrLastAdministrator):
		// 409：请求本身合法，但不变量不允许——与 400（字段非法）区分开，
		// 前端才能给出"先建另一个管理员"的正确引导。
		s.writeAdministrationUserError(
			writer,
			http.StatusConflict,
			users.Messages.LastAdministrator,
		)
	case errors.Is(err, administration.ErrUserNotFound):
		s.writeAdministrationUserError(
			writer,
			http.StatusNotFound,
			users.Messages.UserNotFound,
		)
	case errors.Is(err, administration.ErrSelfDeactivate):
		s.writeAdministrationUserError(
			writer,
			http.StatusBadRequest,
			users.Messages.SelfDeactivate,
		)
	case errors.Is(err, administration.ErrSelfDelete):
		s.writeAdministrationUserError(
			writer,
			http.StatusBadRequest,
			users.Messages.SelfDelete,
		)
	default:
		s.logger.Error("administration user operation", "error", err)
		s.writeAdministrationUserError(
			writer,
			http.StatusInternalServerError,
			users.Messages.InternalError,
		)
	}
}

func administrationMessage(template string, minimum int) string {
	return strings.ReplaceAll(
		template,
		"{minimum}",
		strconv.Itoa(minimum),
	)
}

func (s *Server) writeAdministrationUserError(
	writer http.ResponseWriter,
	status int,
	message string,
) {
	s.writeAdministrationUserJSON(writer, status, map[string]any{
		s.application.Administration.Users.API.Fields.Error: message,
	})
}

func (s *Server) writeAdministrationUserJSON(
	writer http.ResponseWriter,
	status int,
	body map[string]any,
) {
	writer.Header().Set(
		"Cache-Control",
		s.application.Runtime.HTTP.PrivateCacheControl,
	)
	writeJSON(writer, status, body)
}
