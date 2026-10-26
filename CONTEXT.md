# AI 美术资产生成

本上下文描述一个通过可替换外部 AI 供应商，把用户意图转化为平台持久拥有的视觉资产的系统。

## 统一语言

**Capability（能力）**:
与供应商无关的用户目标，例如文生图或图生 3D。
_避免使用_: 任务类型、接口地址

**Provider（供应商）**:
拥有一套上游契约、凭据和运营限制的外部供应方。
_避免使用_: 模型、适配器、模型 ID

**Model（模型）**:
由且仅由一个 Provider 提供的具名生成产品或版本。
_避免使用_: Provider、引擎

**Offering（供给项）**:
一个 Provider、一个 Model 与一个 Capability 的可执行组合，同时定义其输入、策略、预估价格和输出预期。
_避免使用_: Provider 配置、模型选项

**Task（任务）**:
平台持久拥有的一次用户工作，使用独立于任何上游系统的身份。
_避免使用_: 上游作业、请求

**Attempt（尝试）**:
Task 向某个 Offering 发起的一次提交；重试会创建新 Attempt，而不会覆写旧 Attempt。
_避免使用_: 重试次数

**Upstream Job（上游作业）**:
由 Attempt 创建、归 Provider 所有，并在该 Provider 命名空间内标识的异步工作。
_避免使用_: Task

**Artifact Claim（产物声明）**:
Provider 对某个上游结果的暂时性描述，可能包含短时 URL 或内联内容；它必须经过 Materialization 才能成为平台 Asset。
_避免使用_: Asset、最终结果

**Asset（资产）**:
平台拥有持久身份、完整性元数据和访问策略的生成或上传文件。
_避免使用_: CDN URL、结果 URL

**Materialization（资产固化）**:
把 Artifact Claim 读取、校验、哈希并原子写入平台存储，再登记为 Asset 的过程；失败只重试固化，不重新生成。
_避免使用_: 下载一下、Provider 完成

**Task Reference（任务引用）**:
用户用稳定的平台 Task ID 引用一个已成功、属于自己的前置任务；仅 Provider Adapter 可在内部解析它的 Upstream Job。
_避免使用_: 上游任务 ID

**Lease（租约）**:
协调器从持久存储领取下一步动作的限时所有权；它允许多实例和崩溃恢复，但不替代 version CAS 与幂等。
_避免使用_: 进程内运行标记

**Task Success（任务成功）**:
所有必要上游工作均已完成，且每个必要 Asset 都已被平台持久保存时的终态结果。
_避免使用_: 上游成功
