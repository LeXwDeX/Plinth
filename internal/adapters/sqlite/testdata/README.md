# testdata — 历史迁移台账快照

`v1.8.0_schema_migrations.tsv` 是 v1.8.0（提交 `7a510796`，formal-test 实际运行的版本）
`internal/adapters/sqlite/migrations/` 的迁移台账：每行 `版本<TAB>文件名<TAB>sha256`，
共 93 行（0001–0093）。校验和是迁移文件原始字节的 SHA-256，与运行期
`go_schema_migrations.checksum`（`migrationChecksum`）同一口径。

`migration_v180_upgrade_test.go` 用它把「v1.8.0 留下的数据库」固化成可重复的测试：

1. 当前二进制内嵌的 0001–0093 迁移必须与这份台账逐行一致，否则测试直接失败——
   历史迁移一旦被改写，真实存量库的校验和检查也会失败；
2. 用这批迁移建库并写入合成数据，再用当前代码打开同一文件升级 0094 起的新迁移。

因此快照不随新迁移更新：它只描述 v1.8.0，新增版本不会进入这个文件。

## 重新生成

```sh
git ls-tree --name-only 7a510796 internal/adapters/sqlite/migrations/ | sort |
while read -r path; do
  name=$(basename "$path")
  printf '%s\t%s\t%s\n' "${name%%_*}" "$name" \
    "$(git show "7a510796:$path" | shasum -a 256 | cut -d' ' -f1)"
done > internal/adapters/sqlite/testdata/v1.8.0_schema_migrations.tsv
```

快照只含迁移的版本、文件名与校验和，不含 SQL 正文、运行配置或任何业务数据。