import { describe, expect, it } from 'vitest';
import { dataAddressBlob, isDataAddress, sniffImageType, uploadFilename } from './data-address';

describe('data addresses decoded in the page (#187 A)', () => {
  it('decodes base64 bytes and keeps declared parameters out of the type', async () => {
    const blob = dataAddressBlob('data:image/png;name=a.png;base64,iVBORw0KGgo=');
    expect(blob.type).toBe('image/png');
    expect([...new Uint8Array(await blob.arrayBuffer())]).toEqual([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
    expect(dataAddressBlob('data:text/plain;base64,aGk=').type).toBe('text/plain');
    expect(() => dataAddressBlob('data:image/png,raw')).toThrow();
    expect(() => dataAddressBlob('https://x.test/a.png')).toThrow();
    expect(isDataAddress('DATA:image/png;base64,')).toBe(true);
    expect(isDataAddress('blob:x')).toBe(false);
  });
  it('recognises the picture formats by their first bytes', () => {
    expect(sniffImageType(Uint8Array.from([0xff, 0xd8, 0xff, 0xdb]))).toBe('image/jpeg');
    expect(sniffImageType(Uint8Array.from([0x47, 0x49, 0x46, 0x38, 0x39, 0x61]))).toBe('image/gif');
    expect(sniffImageType(Uint8Array.from([1, 2, 3]))).toBe('');
  });
});

describe('upload file names (#187 B)', () => {
  it('keeps a matching original name and fixes a wrong or missing extension', () => {
    expect(uploadFilename('image/jpeg', 'reference_image-1', 'photo.JPEG')).toBe('photo.JPEG');
    expect(uploadFilename('image/jpeg', 'reference_image-1', 'C:\\Users\\me\\photo.jpg')).toBe('photo.jpg');
    expect(uploadFilename('image/png', 'reference_image-1', 'photo.jpg')).toBe('photo.png');
    expect(uploadFilename('image/png', 'reference_image-1', 'photo')).toBe('photo.png');
    expect(uploadFilename('image/png', 'reference_image-1', '.png')).toBe('reference_image-1.png');
    expect(uploadFilename('image/webp', 'reference_image-2')).toBe('reference_image-2.webp');
    expect(uploadFilename('application/x-unknown', 'reference_image-1')).toBe('reference_image-1');
  });
});
