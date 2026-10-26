import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { expect,it,vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import { ResultAttachments } from './result-attachments';
import { isRigCheckReportAsset } from './rig-check-report';
import { projectTaskAssets } from '@/lib/workspace/task-projection';
vi.mock('@/components/product-schema-provider',()=>({useProductSchema:()=>({schema:catalog.application})}));
it('dispatches only exact Rig Check MIME+format to its own viewer and retains authenticated attachment download',()=>{
 const ui=catalog.application.task_history.assets.rig_check_report;
 const assets=projectTaskAssets([{id:'report',role:'other',variant:'report',media_type:ui.media_type,size:80,url:'/api/v1/tasks/t/assets/report'}]);
 const html=renderToStaticMarkup(<ResultAttachments assets={assets} taskId="t" ownerId="owner"/>);
 expect(html).toContain('data-testid="rig-check-report"');expect(html).toContain(ui.loading);expect(html).toContain('href="/api/v1/tasks/t/assets/report"');expect(html).toContain('download=""');expect(html).not.toContain('data-testid="print-report"');expect(html).not.toContain('model-viewer');
 expect(isRigCheckReportAsset(ui.media_type,ui)).toBe(true);
 for(const [mime,presentation] of [['application/json',ui],[ui.media_type,{...ui,format:'v2'}],[ui.media_type,{...ui,media_type:'application/json'}]])expect(isRigCheckReportAsset(mime as string,presentation as typeof ui)).toBe(false);
});
