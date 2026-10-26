// Package etltest 构造旧 Next.js 生产布局的测试 fixture
// （<root>/storage/data/prod.db + storage 六个子目录），
// 供 internal/etl 各票测试与 HTTP 契约测试共用。
package etltest

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// PasswordHash 是 bcrypt("correct-horse")、cost 4 的旧库播种测试哈希
// （先例：httpapi/auth_compat_test.go，跨栈 $2b$ 前缀互认）。
const PasswordHash = "$2b$04$UVs2NmHqVLnDFtkcPXeOhuCLZNX27fLFYc/TbHf7w5wkWsWZXCP2."

// LegacySchema 与 prisma/schema.prisma 的 11 张旧表定义一致
// （DDL 见 prisma/baseline.sql；改动前先对照真源逐列核对）。
const LegacySchema = `
CREATE TABLE "users" (
    "id" TEXT NOT NULL PRIMARY KEY,
    "openId" TEXT,
    "username" TEXT,
    "passwordHash" TEXT NOT NULL DEFAULT '',
    "nickname" TEXT NOT NULL DEFAULT '',
    "email" TEXT NOT NULL DEFAULT '',
    "name" TEXT NOT NULL DEFAULT '',
    "roles" TEXT NOT NULL DEFAULT '',
    "isAdmin" BOOLEAN NOT NULL DEFAULT false,
    "isActive" BOOLEAN NOT NULL DEFAULT true,
    "createdAt" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" DATETIME NOT NULL,
    "deletedAt" DATETIME,
    "lastLogin" DATETIME
);
CREATE TABLE "asset_registry" (
    "id" TEXT NOT NULL PRIMARY KEY,
    "assetType" TEXT NOT NULL,
    "originalFilename" TEXT NOT NULL,
    "storedFilename" TEXT NOT NULL,
    "mimeType" TEXT NOT NULL,
    "fileSize" INTEGER NOT NULL,
    "sourceSha256" TEXT NOT NULL,
    "storedSha256" TEXT NOT NULL,
    "storagePath" TEXT NOT NULL,
    "uploaderUserId" TEXT NOT NULL,
    "uploaderEmail" TEXT NOT NULL DEFAULT '',
    "createdAt" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE "usage_ledger" (
    "id" TEXT NOT NULL PRIMARY KEY,
    "userId" TEXT NOT NULL,
    "userName" TEXT NOT NULL DEFAULT '',
    "userEmail" TEXT NOT NULL DEFAULT '',
    "taskType" TEXT NOT NULL,
    "taskId" TEXT NOT NULL,
    "providerId" TEXT NOT NULL DEFAULT '',
    "estimatedCredits" INTEGER NOT NULL DEFAULT 0,
    "taskStatus" TEXT NOT NULL DEFAULT 'PENDING',
    "params" TEXT NOT NULL DEFAULT '{}',
    "resultUrls" TEXT NOT NULL DEFAULT '[]',
    "retryCount" INTEGER NOT NULL DEFAULT 0,
    "maxRetries" INTEGER NOT NULL DEFAULT 15,
    "nextRetryAt" DATETIME,
    "createdAt" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "finishedAt" DATETIME
);
CREATE TABLE "balance_snapshots" (
    "id" TEXT NOT NULL PRIMARY KEY,
    "balance" REAL NOT NULL,
    "capturedAt" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE "chat_sessions" (
    "id" TEXT NOT NULL PRIMARY KEY,
    "userId" TEXT NOT NULL,
    "userName" TEXT NOT NULL DEFAULT '',
    "title" TEXT NOT NULL DEFAULT '新对话',
    "createdAt" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" DATETIME NOT NULL
);
CREATE TABLE "chat_messages" (
    "id" TEXT NOT NULL PRIMARY KEY,
    "sessionId" TEXT NOT NULL,
    "role" TEXT NOT NULL,
    "content" TEXT NOT NULL DEFAULT '',
    "imageUrls" TEXT NOT NULL DEFAULT '[]',
    "toolCalls" TEXT NOT NULL DEFAULT '[]',
    "createdAt" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT "chat_messages_sessionId_fkey" FOREIGN KEY ("sessionId") REFERENCES "chat_sessions" ("id") ON DELETE CASCADE ON UPDATE CASCADE
);
CREATE TABLE "task_cache" (
    "taskId" TEXT NOT NULL PRIMARY KEY,
    "taskType" TEXT NOT NULL,
    "status" TEXT NOT NULL,
    "providerId" TEXT NOT NULL DEFAULT 'meshy',
    "data" TEXT NOT NULL,
    "cachedAt" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" DATETIME NOT NULL
);
CREATE TABLE "task_input_snapshot" (
    "taskId" TEXT NOT NULL PRIMARY KEY,
    "capability" TEXT NOT NULL,
    "providerId" TEXT NOT NULL,
    "inputParams" TEXT NOT NULL DEFAULT '{}',
    "inputFiles" TEXT NOT NULL DEFAULT '[]',
    "createdAt" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE "audit_logs" (
    "id" TEXT NOT NULL PRIMARY KEY,
    "action" TEXT NOT NULL,
    "userId" TEXT NOT NULL,
    "userName" TEXT NOT NULL DEFAULT '',
    "resourceType" TEXT NOT NULL DEFAULT '',
    "resourceId" TEXT NOT NULL DEFAULT '',
    "detail" TEXT NOT NULL DEFAULT '{}',
    "ipAddress" TEXT NOT NULL DEFAULT '',
    "userAgent" TEXT NOT NULL DEFAULT '',
    "createdAt" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE "canvas_projects" (
    "id" TEXT NOT NULL PRIMARY KEY,
    "userId" TEXT NOT NULL,
    "name" TEXT NOT NULL DEFAULT '未命名项目',
    "sessionId" TEXT,
    "viewport" TEXT NOT NULL DEFAULT '{"zoom":1,"panX":0,"panY":0}',
    "bgStyle" TEXT NOT NULL DEFAULT 'dots',
    "createdAt" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" DATETIME NOT NULL
);
CREATE TABLE "canvas_nodes" (
    "id" TEXT NOT NULL PRIMARY KEY,
    "projectId" TEXT NOT NULL,
    "type" TEXT NOT NULL,
    "x" REAL NOT NULL DEFAULT 0,
    "y" REAL NOT NULL DEFAULT 0,
    "width" REAL NOT NULL DEFAULT 256,
    "height" REAL NOT NULL DEFAULT 256,
    "zIndex" INTEGER NOT NULL DEFAULT 0,
    "data" TEXT NOT NULL DEFAULT '{}',
    "visible" BOOLEAN NOT NULL DEFAULT true,
    "locked" BOOLEAN NOT NULL DEFAULT false,
    "createdAt" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" DATETIME NOT NULL,
    CONSTRAINT "canvas_nodes_projectId_fkey" FOREIGN KEY ("projectId") REFERENCES "canvas_projects" ("id") ON DELETE CASCADE ON UPDATE CASCADE
);
`

// Fixture 是一套按旧 Next 生产布局落盘的旧项目根目录，
// 旧库已建好 11 张表，测试通过 Exec 按需播种数据。
type Fixture struct {
	Root string
	db   *sql.DB
}

func NewFixture(t *testing.T) *Fixture {
	t.Helper()
	root := t.TempDir()
	storage := filepath.Join(root, "storage")
	for _, directory := range []string{
		"data", "assets", "generated", "cache",
		"audit", "thumbnails", "task-inputs",
	} {
		if err := os.MkdirAll(
			filepath.Join(storage, directory), 0o750,
		); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open(
		"sqlite",
		filepath.Join(storage, "data", "prod.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(LegacySchema); err != nil {
		t.Fatal(err)
	}
	return &Fixture{Root: root, db: db}
}

// Exec 在旧库上执行播种语句。
func (f *Fixture) Exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

// DatabasePath 返回旧库文件路径。
func (f *Fixture) DatabasePath() string {
	return filepath.Join(f.Root, "storage", "data", "prod.db")
}
