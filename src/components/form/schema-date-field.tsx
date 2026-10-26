'use client';

import { useProductSchema } from '@/components/product-schema-provider';
import { DateField, type DateFieldProps } from '@/components/ui/date-field';
import { activeLocale } from '@/lib/i18n/locale';
import { schemaText } from '@/lib/product-schema';

/** DateField with its fixed UI text read from the Product Schema. */
export function SchemaDateField(props: Omit<DateFieldProps, 'labels' | 'locale'>) {
  const { schema } = useProductSchema();
  const text = (key: string) => schemaText(schema, key);
  return (
    <DateField
      {...props}
      locale={activeLocale(schema)}
      labels={{
        placeholder: text('ui.common.date.placeholder'),
        previousMonth: text('ui.common.date.previous_month'),
        nextMonth: text('ui.common.date.next_month'),
        today: text('ui.common.date.today'),
        clear: text('ui.common.date.clear'),
      }}
    />
  );
}
