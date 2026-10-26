import type { OfferingWire } from '@/lib/agent-offerings';
import { parameterDisabled, parameterVisible } from '@/lib/product-schema';
import type { ParamSchema } from '@/lib/providers/types';
import { isSourceParameter } from '@/lib/workspace/processing-source';
import { dataAddressBlob, isDataAddress, uploadFilename } from '@/lib/data-address';
import { imageUploadName } from '@/lib/image-upload';

function filled(value: unknown): boolean {
  if (value === undefined || value === null || value === '') return false;
  return !(Array.isArray(value) && value.length === 0);
}

/**
 * Offering 输入规则（`input_rules`）中针对用户可填参数的违例，例如换贴图
 * 的「提示词与风格图恰好填一项」。来源参数由处理计划注入，涉及来源的规则
 * 不在表单层判断。返回每条违例规则涉及的参数名，服务端仍是最终裁决。
 */
export function inputRuleViolations(offering: OfferingWire, input: Record<string, unknown>): string[][] {
  const sources = new Set(offering.parameters.filter(isSourceParameter).map((parameter) => parameter.name));
  const violations: string[][] = [];
  for (const rule of offering.input_rules ?? []) {
    if (rule.parameters.some((name) => sources.has(name))) continue;
    const present = rule.parameters.filter((name) => filled(input[name])).length;
    if (present < rule.minimum || present > rule.maximum) violations.push(rule.parameters);
  }
  return violations;
}

export type AssetUploader = (blob: Blob, filename: string) => Promise<string>;

/**
 * 面板里的图片参数得到的是 data URI；Offering 的资产参数只接受平台输入
 * 资产 ID。提交前把每个 data URI 上传成输入资产并替换为其 ID；`cache`
 * 让同一张图的重试复用同一个 ID，保持计划请求体（及其幂等键）不变。
 */
export async function uploadAssetInputs(
  offering: OfferingWire,
  input: Record<string, unknown>,
  upload: AssetUploader,
  cache: Map<string, string>,
): Promise<Record<string, unknown>> {
  const result: Record<string, unknown> = { ...input };
  const resolve = async (value: unknown, name: string, index: number): Promise<unknown> => {
    if (typeof value !== 'string' || !isDataAddress(value)) return value;
    const known = cache.get(value);
    if (known) return known;
    // Decoded in the page: the CSP connect-src has no data: (#187 A). The name carries the extension of
    // the bytes' real type and keeps the person's own file name when the picker saw one (#187 B).
    const blob = dataAddressBlob(value);
    const id = await upload(blob, uploadFilename(blob.type, `${name}-${index + 1}`, imageUploadName(value)));
    cache.set(value, id);
    return id;
  };
  for (const parameter of offering.parameters) {
    if (isSourceParameter(parameter) || (parameter.type !== 'asset' && parameter.type !== 'asset-array')) continue;
    const value = result[parameter.name];
    if (Array.isArray(value)) {
      const resolved: unknown[] = [];
      for (const [index, item] of value.entries()) resolved.push(await resolve(item, parameter.name, index));
      result[parameter.name] = resolved;
    } else if (value !== undefined) {
      result[parameter.name] = await resolve(value, parameter.name, 0);
    }
  }
  return result;
}

/** 价格区间：基础估算与（存在可选计费选项时）派生的最高估算。 */
export function creditRange(offering: OfferingWire): { low: number; high: number } | null {
  if (offering.price_mode === 'native' || offering.estimated_credits === undefined) return null;
  const high = Math.max(offering.estimated_credits, offering.maximum_estimated_credits ?? 0);
  return { low: offering.estimated_credits, high };
}

/**
 * The visible, enabled required fields of a step that have no value yet (#321): the panel keeps its
 * confirm button disabled and names them, instead of letting the server reject the plan later.
 */
export function missingRequiredParameters(parameters: readonly ParamSchema[], values: Record<string, unknown>): ParamSchema[] {
  return parameters.filter((parameter) => {
    if (!parameter.required || !parameterVisible(parameter, values) || parameterDisabled(parameter, values)) return false;
    const value = values[parameter.name];
    return value === undefined || value === null || value === '' || (Array.isArray(value) && value.length === 0);
  });
}
