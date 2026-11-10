-- 0084: 会话视觉上下文用量的声明估算（#247）。
--
-- context_vision_tokens 是编排器按 Product Schema 声明的每图成本估算的
-- 视觉 token 数（一张联系表或裁剪图也按声明成本计），与上游回报的
-- context_prompt_tokens 分开保存：后者是上游自己的计数，不可比较。
-- 压缩会话时与其它用量字段一起清空，直到下一次请求重新记录。

ALTER TABLE go_chat_sessions ADD COLUMN context_vision_tokens INTEGER;
