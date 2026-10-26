import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import type { OfferingWire } from '@/lib/agent-offerings';
import type { HarnessSource } from '@/lib/harness';
const mock=vi.hoisted(()=>({schema:null as unknown as ProductSchema,controls:new Map<string,(value:string)=>void>(),changed:vi.fn()}));
vi.mock('@/components/product-schema-provider',()=>({useProductSchema:()=>({schema:mock.schema,text:(key:string)=>mock.schema.content.messages[key]||key})}));
vi.mock('@/hooks/use-session-versions',()=>({useSessionVersions:()=>({tiles:[],isLoading:false})}));
vi.mock('@/hooks/use-owned-model-sources',()=>({useOwnedModelSources:()=>({assets:[],page:1,hasNext:false,isLoading:false})}));
vi.mock('@/store/chat-store',()=>({useChatStore:(select:(state:unknown)=>unknown)=>select({activeSessionRevision:1})}));
vi.mock('@/components/ui/option-select',()=>({OptionSelect:({id,value,options,onValueChange}:{id:string;value:string;options:Array<{value:string;label:string}>;onValueChange:(value:string)=>void})=>{mock.controls.set(id.split('-').at(-1)!,onValueChange);return <select value={value} onChange={()=>{}}>{options.map(option=><option key={option.value} value={option.value}>{option.label}</option>)}</select>;}}));
import { HarnessModelSourcePicker,harnessDefaultModelSources } from './harness-model-source-picker';
import { harnessOfferingSelection, harnessAutoSelectOffering } from './harness-panel';
const offering:OfferingWire={id:'o',revision:'1',provider_id:'p',provider_name:'Service',model_id:'m',model_name:'Model',capability:'remesh',available:true,parameters:[{name:'source_task',type:'task',semantic:'source-task',label:'Task'},{name:'source_asset',type:'asset',semantic:'source-asset',label:'Asset'}]};
const render=(source?:HarnessSource)=>renderToStaticMarkup(<HarnessModelSourcePicker ownerId="owner" sessionId="session" object={null} offering={offering} source={source} sourceViews={[]} disabled={false} onChange={mock.changed}/>);
beforeEach(()=>{mock.schema=structuredClone(catalog.application) as unknown as ProductSchema;mock.controls.clear();mock.changed.mockReset();});
describe('Agent model source choice',()=>{
 it('uses the same Schema source default for explicit and unique auto-selected services while preserving saved bounds',()=>{
  const current={session_id:'session',revision:1,enabled_operations:['remesh'],operation_id:'remesh',sources:[],fixed_parameters:{},fillable_parameters:[],allow_prerequisites:false};
  const operation=mock.schema.chat.harness!.operations.find(op=>op.id==='remesh')!;
  const selected=harnessOfferingSelection(current,offering,mock.schema);
  expect(selected.sources).toEqual([{kind:'task_asset',selection_mode:'agent',parameter:'*'}]);
  expect(harnessAutoSelectOffering(current,operation,[offering],false,mock.schema)).toEqual(selected);
  const old={...current,offering_id:offering.id,offering_revision:offering.revision,sources:[{parameter:'source_task',task_id:'old-task'}]};
  expect(harnessAutoSelectOffering(old,operation,[offering],false,mock.schema)).toBe(old);
 });

 it('initializes only a new Offering draft from the declared default, without a model ID or parameter permission edit',()=>{
  expect(harnessDefaultModelSources(mock.schema,offering)).toEqual([{kind:'task_asset',selection_mode:'agent',parameter:'*'}]);
  mock.schema.chat.harness!.default_task_source_mode='fixed';expect(harnessDefaultModelSources(mock.schema,offering)).toEqual([]);
  delete mock.schema.chat.harness!.task_source_modes;expect(harnessDefaultModelSources(mock.schema,offering)).toEqual([]);
  expect(harnessDefaultModelSources(mock.schema,undefined)).toEqual([]);
  expect(harnessDefaultModelSources(mock.schema,{...offering,parameters:[]})).toEqual([]);
 });
 it('shows the declared Agent mode and scope explanation without fixed source controls or the internal wildcard',()=>{
  const html=render({kind:'task_asset',selection_mode:'agent',parameter:'*'});
  expect(html).toContain(mock.schema.chat.harness!.task_source_modes![0].label);
  expect(html).toContain(mock.schema.content.messages['ui.harness.agent_source_help']);
  expect(html).not.toContain('ui.harness.source_binding');expect(html).not.toContain('ui.harness.source_model');expect(html).not.toContain('value="*"');expect(mock.controls.has('source')).toBe(false);
  expect(mock.changed).not.toHaveBeenCalled();
 });
 it.each([undefined,{parameter:'source_task',task_id:'old-task',asset_id:'old-asset'}])('preserves historical fixed/empty sources on render: %s',source=>{
  render(source);expect(mock.controls.has('source')).toBe(true);expect(mock.changed).not.toHaveBeenCalled();
 });
 it('switches explicitly to Agent by clearing all fixed IDs; switching back requires a new choice',()=>{
  render({parameter:'source_task',task_id:'old-task',asset_id:'old-asset'});
  mock.controls.get('mode')!('agent');expect(mock.changed).toHaveBeenLastCalledWith([{kind:'task_asset',selection_mode:'agent',parameter:'*'}]);
  render({kind:'task_asset',selection_mode:'agent',parameter:'*'});
  mock.controls.get('mode')!('fixed');expect(mock.changed).toHaveBeenLastCalledWith([]);
 });
 it('does not pass an Agent wildcard into uploaded source kind or its preview',()=>{
  const html=render({kind:'task_asset',selection_mode:'agent',parameter:'*'});
  expect(html).not.toContain('harness-model-preview-open');mock.controls.get('kind')!('input_asset');expect(mock.changed).toHaveBeenLastCalledWith([]);
 });
});
