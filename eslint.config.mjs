// 前端静态门禁（#436）：只拦 React hook 的调用顺序，不开风格规则。
//
// 规则选择：只把 `react-hooks/rules-of-hooks` 设为 error。它是 #387 聊天区
// React #300（Rendered fewer hooks than expected）的静态对应项——条件、循环、
// 提前 return 之后调用 hook 都会被它报出来。`react-hooks/exhaustive-deps` 只设
// 为 warn、不影响退出码：它是建议性规则，存量代码里不少依赖数组刻意不等同于
// 全部闭包变量（store 快照、稳定回调、ref 转发），改它会改变运行时行为；本门禁
// 只守 #436 的失败模式，不改变依赖数组的既有写法，也不引入第二套风格约束。
import reactHooks from 'eslint-plugin-react-hooks';
import tseslint from 'typescript-eslint';

export default tseslint.config(
  {
    // 构建产物、本地环境文件、Vite 缓存与依赖目录都不属于源码；即使显式传入也不扫。
    ignores: [
      'node_modules/**',
      'build/**',
      'deploy/local/**',
      '**/node_modules/.vite/**',
      '**/node_modules/.vite-temp/**',
    ],
  },
  {
    files: ['src/**/*.{ts,tsx}', 'web/**/*.{ts,tsx}'],
    languageOptions: {
      // 源码全是 TypeScript/TSX，用 typescript-eslint 的 parser 解析才能看到 .tsx 里的 hook 调用。
      parser: tseslint.parser,
      parserOptions: {
        ecmaVersion: 'latest',
        sourceType: 'module',
      },
    },
    plugins: {
      'react-hooks': reactHooks,
    },
    rules: {
      'react-hooks/rules-of-hooks': 'error',
      'react-hooks/exhaustive-deps': 'warn',
    },
  },
);