import { deflateSync } from 'node:zlib';
function crc32(buf){let c=0xffffffff;for(const b of buf){c^=b;for(let i=0;i<8;i++)c=(c>>>1)^(0xedb88320&-(c&1));}return (c^0xffffffff)>>>0;}
function chunk(t,d){const n=Buffer.from(t,'ascii'),l=Buffer.alloc(4);l.writeUInt32BE(d.length);const c=Buffer.alloc(4);c.writeUInt32BE(crc32(Buffer.concat([n,d])));return Buffer.concat([l,n,d,c]);}
// left half red-ish, right half blue-ish test PNG
export function makePng(w,h,left=[220,60,40],right=[40,60,220]){
  const ihdr=Buffer.alloc(13);ihdr.writeUInt32BE(w,0);ihdr.writeUInt32BE(h,4);ihdr[8]=8;ihdr[9]=6;
  const stride=w*4+1;const raw=Buffer.alloc(stride*h);
  for(let y=0;y<h;y++){raw[y*stride]=0;for(let x=0;x<w;x++){const o=y*stride+1+x*4;const c=x<w/2?left:right;raw[o]=c[0];raw[o+1]=c[1];raw[o+2]=c[2];raw[o+3]=255;}}
  return Buffer.concat([Buffer.from([137,80,78,71,13,10,26,10]),chunk('IHDR',ihdr),chunk('IDAT',deflateSync(raw)),chunk('IEND',Buffer.alloc(0))]);
}
// minimal self-contained GLB (one triangle, embedded BIN chunk). pad to target bytes if given.
export function makeGlb(padTo=0, externalUri=null){
  const pos=Buffer.alloc(36);[0,0,0,1,0,0,0,1,0].forEach((v,i)=>pos.writeFloatLE(v,i*4));
  const json={asset:{version:'2.0'},scene:0,scenes:[{nodes:[0]}],nodes:[{mesh:0}],
    meshes:[{primitives:[{attributes:{POSITION:0}}]}],
    buffers:[externalUri?{uri:externalUri,byteLength:36}:{byteLength:36}],
    bufferViews:[{buffer:0,byteLength:36,target:34962}],
    accessors:[{bufferView:0,componentType:5126,count:3,type:'VEC3',min:[0,0,0],max:[1,1,0]}]};
  let js=Buffer.from(JSON.stringify(json));while(js.length%4)js=Buffer.concat([js,Buffer.from(' ')]);
  let bin=pos;
  if(padTo){const extra=Math.max(0,padTo-(12+8+js.length+8+36));bin=Buffer.concat([pos,Buffer.alloc(extra)]);json.buffers[0].byteLength=bin.length;
    js=Buffer.from(JSON.stringify(json));while(js.length%4)js=Buffer.concat([js,Buffer.from(' ')]);}
  while(bin.length%4)bin=Buffer.concat([bin,Buffer.alloc(1)]);
  const total=12+8+js.length+8+bin.length;
  const h=Buffer.alloc(12);h.writeUInt32LE(0x46546c67,0);h.writeUInt32LE(2,4);h.writeUInt32LE(total,8);
  const jh=Buffer.alloc(8);jh.writeUInt32LE(js.length,0);jh.writeUInt32LE(0x4e4f534a,4);
  const bh=Buffer.alloc(8);bh.writeUInt32LE(bin.length,0);bh.writeUInt32LE(0x004e4942,4);
  return Buffer.concat([h,jh,js,bh,bin]);
}
