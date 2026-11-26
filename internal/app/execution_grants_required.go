package app

import "errors"

// errExecutionGrantsUnwired refuses to start an application whose generation
// tools would submit paid steps without an execution grant (#665).
var errExecutionGrantsUnwired = errors.New("生成工具未接入执行授权（prepared gateway、操作存储、授权服务或停止读取缺失），拒绝启动")

// grantWiring is what requireExecutionGrants needs from the generation tools.
type grantWiring interface{ UsesExecutionGrants() bool }

// requireExecutionGrants fails closed when the generation tools lack any port
// of the execution grant path: every Agent paid step must be prepared, granted
// by a person or the session policy, and claimed in the Task transaction.
func requireExecutionGrants(tools grantWiring) error {
	if tools == nil || !tools.UsesExecutionGrants() {
		return errExecutionGrantsUnwired
	}
	return nil
}
