-- 0092: 完成观察携带的公开提示（#284）。Provider 的完成观察可以说明“任务成功，
-- 但有一部分没有做出来”（例如抠图批次里有几张没抠成）：内核把规范化后的提示
-- （JSON 数组，如 [{"code":"partial_inputs","inputs":[2,4]}]）与完成的 Attempt
-- 一起写入，只写这一次。NULL 表示该 Attempt 没有提示。任务读取只对 SUCCEEDED
-- 的任务投影当前 Attempt 的这一列；历史行为 NULL，零行为变化。
ALTER TABLE generation_attempts ADD COLUMN notices_json TEXT;
