import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import { parseNormalizedRigCheckReport, readRigCheckReport, RigCheckReportContent } from './rig-check-report';
import { projectTaskAssets } from '@/lib/workspace/task-projection';
import { parseLineage } from '@/lib/workspace/lineage';
import { buildVersionTiles } from '@/lib/workspace/versions';
import { objectOfTarget } from '@/lib/workspace/result-target';
import type { ProductSchema } from '@/lib/product-schema';
const ui=catalog.application.task_history.assets.rig_check_report;
const valid={format:'art-ai.rig-check-report.v1' as const,riggable:false,rig_type:'biped'};
afterEach(()=>vi.unstubAllGlobals());
it('preserves a completed negative check and its required recommendation without manufacturing a rig or failure',()=>{
 expect(parseNormalizedRigCheckReport(valid)).toEqual(valid);
 const html=renderToStaticMarkup(<RigCheckReportContent report={valid} presentation={ui}/>);
 expect(html).toContain(ui.boolean_labels.false);expect(html).toContain(ui.species_labels.biped);expect(html).toContain(ui.limitation);
 expect(html).not.toContain('role="alert"');expect(html).not.toContain('model-viewer');
 const renamed={...ui,field_labels:{riggable:'Schema compatibility',rig_type:'Schema recommendation'},boolean_labels:{true:'Schema yes',false:'Schema no'}};
 const changed=renderToStaticMarkup(<RigCheckReportContent report={valid} presentation={renamed}/>);expect(changed).toContain('Schema no');expect(changed).toContain('Schema recommendation');
});
it('accepts only this normalized version with exact boolean and bounded declared species, not upstream wrappers',()=>{
 for(const riggable of [false,true]) {
  for(const rig_type of ['biped','quadruped','hexapod','octopod','avian','serpentine','aquatic'])expect(parseNormalizedRigCheckReport({...valid,riggable,rig_type})?.rig_type).toBe(rig_type);
  expect(parseNormalizedRigCheckReport({format:valid.format,riggable})).toBeNull();
  for(const rig_type of [undefined,null,'','humanoid'])expect(parseNormalizedRigCheckReport({...valid,riggable,rig_type})).toBeNull();
 }
 for(const raw of [{format:valid.format,riggable:false},{...valid,rig_type:''},{...valid,format:'v2'},{format:valid.format},{...valid,riggable:null},{...valid,riggable:'false'},{...valid,rig_type:null},{...valid,rig_type:'humanoid'},{...valid,url:'/secret'},{output:{riggable:true,rig_type:'biped'}},{...valid,kind:'analysis'}])expect(parseNormalizedRigCheckReport(raw)).toBeNull();
});
it('fetches bounded authenticated same-origin report bytes without following redirects',async()=>{
 const fetch=vi.fn().mockResolvedValue(new Response(JSON.stringify(valid),{headers:{'content-type':ui.media_type}}));vi.stubGlobal('fetch',fetch);
 expect(await readRigCheckReport('/api/v1/tasks/task/assets/report',ui)).toEqual(valid);
 expect(fetch).toHaveBeenCalledWith('/api/v1/tasks/task/assets/report',expect.objectContaining({credentials:'same-origin',redirect:'error'}));
 await expect(readRigCheckReport('https://other.invalid/r',ui)).rejects.toThrow();expect(fetch).toHaveBeenCalledTimes(1);
});
it('rejects ambiguous, oversized, truncated, wrong-MIME and unauthenticated bytes without defaulting false',async()=>{
 const bodies=[new Response('{}',{status:403}),new Response(JSON.stringify(valid),{headers:{'content-type':'application/json'}}),...[' '.repeat(ui.max_bytes+1),'{"format":"art-ai.rig-check-report.v1","riggable":false,"riggable":true,"rig_type":"biped"}','{"format":"art-ai.rig-check-report.v1","riggable":false,"rig_type":"biped","rig_type":"avian"}','{"format":', '{"format":"art-ai.rig-check-report.v1","riggable":false,"rigg\\u0061ble":true,"rig_type":"biped"}'].map(b=>new Response(b,{headers:{'content-type':ui.media_type}}))];
 for(const response of bodies){vi.stubGlobal('fetch',vi.fn().mockResolvedValue(response));await expect(readRigCheckReport('/api/v1/tasks/task/assets/report',ui)).rejects.toThrow()}
});
it('a report is an honest downloadable attachment, never a READY media version or CurrentObject',()=>{
 const raw={id:'report',role:'other',variant:'report',media_type:ui.media_type,size:80,url:'/api/v1/tasks/t/assets/report'};
 expect(projectTaskAssets([raw])[0].kind).toBe('other');
 const graph=parseLineage({nodes:[{id:'t',kind:'task',task_id:'t',capability:'rig-check',state:'SUCCEEDED',assets:[raw]}],edges:[]});
 expect(buildVersionTiles({schema:catalog.application as unknown as ProductSchema,graph,tracked:[],sessionId:'s'}).tiles).toHaveLength(0);
 expect(objectOfTarget({taskId:'t',kind:'other',url:raw.url,title:'check'})).toBeNull();
});
