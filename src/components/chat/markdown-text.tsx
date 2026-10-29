import { Fragment, useCallback, useMemo, type ReactNode } from 'react';

import { HandleTag } from '@/components/chat/handle-tag';
import { splitHandles, type ComposerReference } from '@/lib/chat/composer-reference';
import { parseMarkdown, type MarkdownBlock, type MarkdownInline } from '@/lib/chat/markdown';

type References = ReadonlyMap<string, ComposerReference> | undefined;

/** Message text with every handle of a session picture ("@img3") as a tag that attaches the picture to the next message. */
export function MessageText({ text, references }: { text: string; references?: ReadonlyMap<string, ComposerReference> }) {
  if (!references || references.size === 0) return <>{text}</>;
  return (
    <>
      {splitHandles(text, (handle) => references.has(handle)).map((segment, index) => {
        const reference = segment.handle ? references.get(segment.handle) : undefined;
        return reference
          ? <HandleTag key={index} reference={reference} testId="chat-text-handle" className="inline px-0.5 py-0 text-[13px] font-medium leading-[inherit] text-primary hover:underline hover:ring-0" />
          : <span key={index}>{segment.text}</span>;
      })}
    </>
  );
}

/** The Markdown source each rendered reply was made from, for a copy that takes the whole reply (#333). */
const sources = new WeakMap<Element, string>();

/** The Markdown source of a rendered reply (an element marked `data-chat-markdown`). */
export function markdownSourceOf(element: Element): string | undefined {
  return sources.get(element);
}

/**
 * An Agent reply as its Markdown subset (#333). Every part is a React element or text node made from the parsed
 * tree, never HTML from the reply. `tail` (the streaming spinner) sits at the end of the last words.
 */
export function MarkdownText({ text, streaming = false, references, tail }: {
  text: string;
  streaming?: boolean;
  references?: ReadonlyMap<string, ComposerReference>;
  tail?: ReactNode;
}) {
  const blocks = useMemo(() => parseMarkdown(text, { streaming }), [text, streaming]);
  const remember = useCallback((element: HTMLDivElement | null) => {
    if (element) sources.set(element, text);
  }, [text]);
  return (
    <div data-chat-markdown="" ref={remember} className="space-y-2">
      <Blocks blocks={blocks} references={references} tail={tail} />
    </div>
  );
}

function Blocks({ blocks, references, tail }: { blocks: MarkdownBlock[]; references: References; tail?: ReactNode }) {
  return (
    <>
      {blocks.map((block, index) => (
        <Block key={index} block={block} references={references} tail={index === blocks.length - 1 ? tail : undefined} />
      ))}
      {blocks.length === 0 ? tail : null}
    </>
  );
}

function Block({ block, references, tail }: { block: MarkdownBlock; references: References; tail?: ReactNode }) {
  switch (block.type) {
    case 'paragraph':
      return <p className="whitespace-pre-wrap"><Inlines nodes={block.children} references={references} />{tail}</p>;
    case 'heading':
      return <p className="whitespace-pre-wrap font-semibold"><Inlines nodes={block.children} references={references} />{tail}</p>;
    case 'code':
      return (
        <>
          <pre className="overflow-x-auto rounded-md bg-muted px-3 py-2 font-mono text-[12px] leading-relaxed [overflow-wrap:normal]"><code>{block.text}</code></pre>
          {tail}
        </>
      );
    case 'table': {
      const alignClass = (align: typeof block.align[number]) => align === 'center' ? 'text-center' : align === 'right' ? 'text-right' : 'text-left';
      const last = block.rows.length - 1;
      // The wrapper scrolls on its own, so a wide table never widens the message bubble.
      return (
        <div className="max-w-full overflow-x-auto" data-testid="chat-markdown-table">
          <table className="min-w-full border-collapse text-[12px] leading-snug [overflow-wrap:normal]">
            <thead>
              <tr>
                {block.header.map((cell, index) => (
                  <th key={index} scope="col" className={`min-w-[6rem] border border-border bg-muted px-2 py-1 font-semibold ${alignClass(block.align[index])}`}>
                    <Inlines nodes={cell} references={references} />
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {block.rows.map((row, rowIndex) => (
                <tr key={rowIndex}>
                  {row.map((cell, index) => (
                    <td key={index} className={`min-w-[6rem] border border-border px-2 py-1 align-top ${alignClass(block.align[index])}`}>
                      <Inlines nodes={cell} references={references} />
                      {rowIndex === last && index === row.length - 1 ? tail : null}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
          {block.rows.length === 0 ? tail : null}
        </div>
      );
    }
    case 'rule':
      return <><hr className="border-border" />{tail}</>;
    case 'list': {
      const items = block.items.map((item, index) => (
        <li key={index} className="space-y-1">
          <Blocks blocks={item} references={references} tail={index === block.items.length - 1 ? tail : undefined} />
        </li>
      ));
      return block.ordered
        ? <ol className="list-decimal space-y-1 pl-5" start={block.start === 1 ? undefined : block.start}>{items}</ol>
        : <ul className="list-disc space-y-1 pl-5">{items}</ul>;
    }
  }
}

function Inlines({ nodes, references }: { nodes: MarkdownInline[]; references: References }) {
  return (
    <>
      {nodes.map((node, index) => {
        switch (node.type) {
          case 'text':
            return <MessageText key={index} text={node.text} references={references} />;
          case 'strong':
            return <strong key={index} className="font-semibold"><Inlines nodes={node.children} references={references} /></strong>;
          case 'em':
            return <em key={index}><Inlines nodes={node.children} references={references} /></em>;
          case 'code':
            return <code key={index} className="rounded-sm bg-muted px-1 py-0.5 font-mono text-[12px]">{node.text}</code>;
          case 'link':
            // Words only inside a link: a handle tag is a button, which cannot sit in an anchor.
            return (
              <a key={index} href={node.href} target="_blank" rel="noopener noreferrer" className="text-primary underline underline-offset-2 hover:opacity-80">
                <Inlines nodes={node.children} references={undefined} />
              </a>
            );
          default:
            return <Fragment key={index} />;
        }
      })}
    </>
  );
}
