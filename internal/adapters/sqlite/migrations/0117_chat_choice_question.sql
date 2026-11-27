-- 0117: 通用选择卡保存 Agent 自己写的问题（#699）。
--
-- ask_choice 的 question 原先只进工具结果，卡片显示 kind 的默认问题。新列保存
-- Agent 给出、不超过工具声明长度的问题；空串表示沿用 kind 的默认问题。
-- 只新增列，旧卡片取值为空串，照旧显示默认问题。
ALTER TABLE chat_choices ADD COLUMN question TEXT NOT NULL DEFAULT '';
