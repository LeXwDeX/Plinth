'use client';

import Link from '@/components/ui/browser-link';
import { FileQuestion } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { useProductSchema } from '@/components/product-schema-provider';

export default function NotFound() {
  const { text } = useProductSchema();
  return (
    <div className="flex min-h-[60vh] flex-col items-center justify-center text-center">
      <FileQuestion className="h-16 w-16 text-muted-foreground/30 mb-6" />
      <h2 className="text-2xl font-bold mb-2">
        {text('ui.boundary.not_found.title')}
      </h2>
      <p className="text-muted-foreground mb-6">
        {text('ui.boundary.not_found.description')}
      </p>
      <Button render={<Link href="/" />}>
        {text('ui.boundary.not_found.home')}
      </Button>
    </div>
  );
}
