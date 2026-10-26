package httpapi_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/authentication"
)

// TestLegacyLoginRejectsIdentityIncompleteAccountAsUnauthorized：口令正确但
// 身份不完整（缺邮箱）的存量账号属于凭据问题，必须返 401 并复用"用户名或密码
// 错误"文案——旧行为把它报成 500，既误导用户又无法归因。
func TestLegacyLoginRejectsIdentityIncompleteAccountAsUnauthorized(
	t *testing.T,
) {
	t.Parallel()
	now := time.Date(2026, 7, 26, 18, 0, 0, 0, time.UTC)
	record := authentication.UserRecord{
		ID: "no-email-user", Username: "legacy-no-email",
		// 与 TestLegacyLoginAndSessionMatchNextContract 同一份
		// "correct-horse" 的 bcrypt 结果。
		PasswordHash: "$2b$04$UVs2NmHqVLnDFtkcPXeOhuCLZNX27fLFYc/TbHf7w5wkWsWZXCP2.",
		Nickname:     "无邮箱账号", Email: "",
		Admin: false, Active: true,
	}
	directory := &authDirectory{
		byUsername: map[string]authentication.UserRecord{
			record.Username: record,
		},
		byID: map[string]authentication.UserRecord{record.ID: record},
	}
	_, handler, _ := newHTTPHarnessWithAuthentication(
		t,
		[]string{"user"},
		nil,
		func(config *authentication.Config) {
			config.Directory = directory
			config.SessionSecret = strings.Repeat("l", 32)
			config.Clock = func() time.Time { return now }
		},
	)

	assertJSONResponse(
		t,
		handler,
		http.MethodPost,
		"/api/auth/login",
		`{"username":"legacy-no-email","password":"correct-horse"}`,
		http.StatusUnauthorized,
		map[string]any{"error": "用户名或密码错误"},
	)

	// 该失败必须计入限流计数（否则身份残缺账号可被无限尝试）。
	throttled := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/login",
		bytes.NewBufferString(
			`{"username":"legacy-no-email","password":"wrong"}`,
		),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Forwarded-For", "203.0.113.77")
	handler.ServeHTTP(throttled, request)
	if throttled.Code == http.StatusOK {
		t.Fatal("错误口令在失败记账后仍被接受")
	}
}

// TestLegacyLoginKeepsServerFaultsAsInternalError：真正的服务端故障仍必须是
// 500——身份类降级不能顺手把故障也吞成 401。
func TestLegacyLoginKeepsServerFaultsAsInternalError(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithAuthentication(
		t,
		[]string{"user"},
		nil,
		func(config *authentication.Config) {
			config.Directory = failingAuthDirectory{}
			config.SessionSecret = strings.Repeat("l", 32)
		},
	)

	assertJSONResponse(
		t,
		handler,
		http.MethodPost,
		"/api/auth/login",
		`{"username":"broken","password":"correct-horse"}`,
		http.StatusInternalServerError,
		map[string]any{"error": "登录失败"},
	)
}

type failingAuthDirectory struct{}

func (failingAuthDirectory) FindActiveByUsername(
	context.Context,
	string,
) (authentication.UserRecord, bool, error) {
	return authentication.UserRecord{}, false, errDirectoryUnavailable
}

func (failingAuthDirectory) FindByID(
	context.Context,
	string,
) (authentication.UserRecord, bool, error) {
	return authentication.UserRecord{}, false, errDirectoryUnavailable
}

func (failingAuthDirectory) UpdateLastLogin(
	context.Context,
	string,
	time.Time,
) error {
	return errDirectoryUnavailable
}

var errDirectoryUnavailable = &directoryError{}

type directoryError struct{}

func (*directoryError) Error() string {
	return "directory unavailable"
}
