/**
 * 统一 Provider 类型定义
 * 所有 API 来源（Meshy、GPT-IMAGE、未来的其他厂商）统一实现此体系
 */

/** Provider 参数定义（前端按此动态渲染表单） */
export interface ParamOption {
  value: string;
  label: string;
  group?: string;
}

export interface ParamSchema {
  name: string;
  label: string;
  type: 'select' | 'text' | 'boolean' | 'number' | 'slider' | 'asset' | 'string-array' | 'multi-select';
  validation?: {
    type: string;
    max_length?: number;
    max_bytes?: number;
    media_types?: string[];
    min_items?: number;
    max_items?: number;
    integer?: boolean;
    image_size?: { min_pixels: number; max_pixels: number; max_edge?: number; multiple?: number; max_ratio: number };
  };
  options?: ParamOption[];
  default?: unknown;
  required?: boolean;
  /** 数值类型最小值约束 */
  min?: number;
  /** 数值类型最大值约束 */
  max?: number;
  /** 数值类型步长 */
  step?: number;
  /** 参数说明（前端展示用） */
  description?: string;
  /** Schema presentation.surface：canvas=画板区就地渲染，provider=参数面板 */
  surface?: 'canvas' | 'provider';
  /**
   * 安全派生标记：该参数参与额外积分计费（extended 投影由
   * cost.extra_when 派生；仅用于估算展示与同意门槛，不承载金额）。
   */
  costExtra?: boolean;
  /** 声明式禁用条件；由通用参数渲染器执行 */
  disabled_when?: ParamCondition[];
  /**
   * 声明式可见条件；全部条件满足时控件才渲染（AND 语义）。
   * 隐藏参数不进入提交值（与服务端默认值/强制值裁决对齐）。
   */
  visible_when?: ParamCondition[];
  /**
   * 声明式强制值；服务端 normalize 拥有最终裁决（W2 契约）。
   * 前端只用于展示锁定与提交对齐，不得发明新强制规则。
   */
  forced_when?: ParamForcedValue[];
  /** 声明式参数提示；显示条件、文案键和语气均来自 Offering Schema */
  notices?: ParamNotice[];
}

export interface ParamCondition {
  parameter: string;
  operator:
    | 'equals'
    | 'not-equals'
    | 'truthy'
    | 'falsy'
    | 'present'
    | 'absent';
  value?: unknown;
}

export interface ParamForcedValue {
  when: ParamCondition;
  value: unknown;
}

export interface ParamNotice {
  when: ParamCondition;
  message_key: string;
  tone: 'info' | 'warning' | 'error';
}
