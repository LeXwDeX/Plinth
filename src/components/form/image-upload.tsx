'use client';

import { useState, useRef, useCallback, useId } from 'react';
import { cn } from '@/lib/utils';
import { Upload, X, ImagePlus } from 'lucide-react';
import { useProductSchema } from '@/components/product-schema-provider';
import { schemaCountMessage, schemaMessage } from '@/lib/product-schema';
import { imageUploadAccept, ImageUploadError, prepareImageUpload } from '@/lib/image-upload';
import { Action } from '@/components/ui/button';
import { formatSizeLimit } from '@/lib/size-text';
import type { ProductSchema } from '@/lib/product-schema';

/**
 * The formats an upload control states for the parameter it fills (#329 follow-up): the media types the Schema
 * declares for that parameter (`accept`), each named by its Schema label (`ui.form.image_upload.format.<subtype>`,
 * the subtype in capitals when none is declared). Empty when the control has no target parameter.
 */
export function declaredFormats(schema: ProductSchema, accept: string | undefined): string {
  const messages = schema.content.messages as Record<string, string | undefined>;
  const names = (accept ?? '').split(',').map((item) => item.trim().toLowerCase())
    .filter((item) => item.startsWith('image/'))
    .map((item) => {
      const subtype = item.slice('image/'.length);
      return messages[`ui.form.image_upload.format.${subtype}`] || subtype.toUpperCase();
    });
  return [...new Set(names)].join(messages['ui.form.image_upload.format_separator'] ?? ', ');
}

interface ImageUploadProps {
  id?: string;
  'aria-describedby'?: string;
  'aria-invalid'?: boolean;
  onUpload: (dataUris: string[]) => void;
  accept?: string;
  multiple?: boolean;
  maxFiles?: number;
  /** The upload limit in bytes; the Schema's upload limit when absent. */
  maxBytes?: number;
  disabled?: boolean;
  className?: string;
}

export function ImageUpload({
  id,
  'aria-describedby': describedBy,
  'aria-invalid': invalid,
  onUpload,
  accept,
  multiple = false,
  maxFiles,
  maxBytes: maxBytesProp,
  disabled = false,
  className,
}: ImageUploadProps) {
  const generatedId = useId();
  const inputId = id ?? generatedId;
  const errorId = `${inputId}-upload-error`;
  const { schema, text } = useProductSchema();
  const maxBytes = maxBytesProp ?? schema.runtime.input_assets.upload_max_bytes;
  // The limit as every size is written: decimal MB with the unit (#327).
  const maxSizeText = formatSizeLimit(maxBytes);
  const formats = declaredFormats(schema, accept);
  const imageRule = schema.runtime.input_assets.type_rules.find(
    (rule) => rule.asset_type === schema.runtime.input_assets.default_asset_type,
  );
  const acceptedOutput = accept ?? imageRule?.media_types?.join(',');
  const uploadPolicy = schema.runtime.frontend.image_upload;
  const configuredAccept = imageUploadAccept(acceptedOutput, uploadPolicy);
  const [previews, setPreviews] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [isDragging, setIsDragging] = useState(false);
  const [isProcessing, setIsProcessing] = useState(false);
  const previewsRef = useRef<string[]>([]);
  const processingRef = useRef(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const uploadDisabled = disabled || isProcessing;
  const uploadDescription = [describedBy, error ? errorId : undefined].filter(Boolean).join(' ') || undefined;

  const processFiles = useCallback(
    async (files: FileList | File[]) => {
      if (disabled || processingRef.current) return;
      processingRef.current = true;
      setIsProcessing(true);
      setError(null);
      const fileArray = Array.from(files);
      const filesToProcess = multiple ? fileArray : fileArray.slice(0, 1);
      let filename = '';
      try {
        const dataUris: string[] = [];
        // Decode sequentially so a multi-image drop retains only one conversion canvas.
        for (const file of filesToProcess) {
          filename = file.name;
          dataUris.push(await prepareImageUpload(file, {
            accept: acceptedOutput,
            maximumBytes: maxBytes,
            policy: uploadPolicy,
            // The field's picture may become an input asset later, under the person's file name (#187 B).
            rememberName: true,
          }));
        }
        if (multiple) {
          let next = [...previewsRef.current, ...dataUris];
          // 如果指定了 maxFiles，截断到上限
          if (maxFiles !== undefined && next.length > maxFiles) {
            next = next.slice(0, maxFiles);
            setError(schemaCountMessage(schema, 'ui.form.image_upload.maximum', maxFiles, {
              count: maxFiles,
            }));
          }
          previewsRef.current = next;
          setPreviews(next);
          onUpload(next);
        } else {
          previewsRef.current = dataUris;
          setPreviews(dataUris);
          onUpload(dataUris);
        }
      } catch (error) {
        const code = error instanceof ImageUploadError ? error.code : 'read_failed';
        setError(schemaMessage(schema, `ui.form.image_upload.${code}`, {
          filename,
          size: maxSizeText,
          pixels: uploadPolicy.maximum_pixels,
        }));
      } finally {
        processingRef.current = false;
        setIsProcessing(false);
      }
    },
    [
      acceptedOutput,
      maxBytes,
      maxSizeText,
      disabled,
      maxFiles,
      multiple,
      onUpload,
      schema,
      uploadPolicy,
    ],
  );

  const handleDragOver = useCallback(
    (e: React.DragEvent) => {
      e.preventDefault();
      if (!uploadDisabled) setIsDragging(true);
    },
    [uploadDisabled]
  );

  const handleDragLeave = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    setIsDragging(false);
  }, []);

  const handleDrop = useCallback(
    (e: React.DragEvent) => {
      e.preventDefault();
      setIsDragging(false);
      if (uploadDisabled) return;
      if (e.dataTransfer.files.length > 0) {
        processFiles(e.dataTransfer.files);
      }
    },
    [uploadDisabled, processFiles]
  );

  const handleFileChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      if (e.target.files && e.target.files.length > 0) {
        processFiles(e.target.files);
      }
      // Reset input so same file can be re-selected
      e.target.value = '';
    },
    [processFiles]
  );

  const removePreview = useCallback(
    (index: number) => {
      const next = previewsRef.current.filter((_, i) => i !== index);
      previewsRef.current = next;
      setPreviews(next);
      onUpload(next);
    },
    [onUpload]
  );

  return (
    <div className={cn('flex flex-col gap-3', className)}>
      {/* Drop zone */}
      <button
        type="button"
        onClick={() => !uploadDisabled && inputRef.current?.click()}
        onDragOver={handleDragOver}
        onDragLeave={handleDragLeave}
        onDrop={handleDrop}
        disabled={uploadDisabled}
        aria-describedby={uploadDescription}
        aria-invalid={invalid || Boolean(error) || undefined}
        className={cn(
          'flex min-h-32 w-full cursor-pointer flex-col items-center justify-center gap-2 rounded-lg border-2 border-dashed p-6 text-center transition-colors focus-visible:outline-none focus-visible:ring-3 focus-visible:ring-ring/50',
          isDragging
            ? 'border-primary bg-primary/10'
            : 'border-border hover:border-primary',
          uploadDisabled && 'cursor-not-allowed opacity-50'
        )}
      >
        {isDragging ? (
          <Upload aria-hidden="true" className="h-8 w-8 text-primary" />
        ) : (
          <ImagePlus aria-hidden="true" className="h-8 w-8 text-muted-foreground" />
        )}
        <p className="text-sm text-muted-foreground">
          {isDragging
            ? text('ui.form.image_upload.release')
            : text('ui.form.image_upload.drop')}
        </p>
        <p className="text-xs text-muted-foreground">
          {/* A control for a declared parameter states that parameter's formats; the general one keeps its text. */}
          {formats
            ? schemaMessage(schema, 'ui.form.image_upload.supported_formats', { formats, size: maxSizeText })
            : schemaMessage(schema, 'ui.form.image_upload.supported', { size: maxSizeText })}
          {multiple &&
            (maxFiles
              ? schemaCountMessage(schema, 'ui.form.image_upload.maximum_hint', maxFiles, {
                  count: maxFiles,
                })
              : text('ui.form.image_upload.multiple_hint'))}
        </p>
      </button>

      <input
        ref={inputRef}
        id={inputId}
        type="file"
        accept={configuredAccept}
        multiple={multiple}
        onChange={handleFileChange}
        className="hidden"
        disabled={uploadDisabled}
        aria-describedby={uploadDescription}
        aria-invalid={invalid || Boolean(error) || undefined}
      />

      {/* Error message */}
      {error && (
        <p id={errorId} role="alert" className="text-xs text-destructive">{error}</p>
      )}

      {/* Previews */}
      {previews.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {previews.map((uri, index) => (
            <div
              key={index}
              className="group relative h-20 w-20 overflow-hidden rounded-lg border bg-muted"
            >
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img
                src={uri}
                alt={schemaMessage(schema, 'ui.form.image_upload.preview_alt', {
                  index: index + 1,
                })}
                className="h-full w-full object-cover"
              />
              {multiple && previews.length > 1 && (
                <span className="absolute left-1 top-1 rounded bg-black/60 px-1 py-0.5 text-[10px] font-bold leading-none text-foreground">
                  #{index + 1}
                </span>
              )}
              <Action
                type="button"
                variant="media"
                size="icon-xs"
                aria-label={schemaMessage(
                  schema,
                  'ui.form.image_upload.remove',
                  { index: index + 1 },
                )}
                onClick={(e) => {
                  e.stopPropagation();
                  removePreview(index);
                }}
                disabled={uploadDisabled}
                className="absolute right-0.5 top-0.5 rounded-full opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 focus-visible:opacity-100"
              >
                <X className="h-3 w-3" />
              </Action>
            </div>
          ))}
        </div>
      )}
      {multiple && previews.length > 1 && (
        <p className="text-xs text-muted-foreground">
          {text('ui.form.image_upload.multi_role_hint')}
        </p>
      )}
    </div>
  );
}
