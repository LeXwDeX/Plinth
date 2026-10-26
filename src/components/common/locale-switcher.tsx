'use client';

import { Languages } from 'lucide-react';

import { useProductSchema } from '@/components/product-schema-provider';
import {
  DropdownMenuGroup,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
} from '@/components/ui/dropdown-menu';
import { OptionSelect } from '@/components/ui/option-select';
import { activeLocale, chooseLocale, localeOptions } from '@/lib/i18n/locale';

/** The language choice inside the user menu; renders nothing for a single-locale Schema. */
export function LocaleMenuGroup() {
  const { schema, text } = useProductSchema();
  const options = localeOptions(schema);
  if (options.length < 2) return null;
  return (
    <>
      <DropdownMenuSeparator />
      <DropdownMenuGroup>
        <DropdownMenuLabel className="flex items-center gap-1.5">
          <Languages className="size-3.5" aria-hidden="true" />
          {text('ui.common.language')}
        </DropdownMenuLabel>
        <DropdownMenuRadioGroup
          value={activeLocale(schema)}
          onValueChange={(locale) => chooseLocale(schema, String(locale))}
        >
          {options.map((option) => (
            <DropdownMenuRadioItem key={option.id} value={option.id} lang={option.id} data-testid={`locale-option-${option.id}`}>
              {option.label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuGroup>
    </>
  );
}

/** A compact language select for pages without the user menu, such as sign-in. */
export function LocaleSelect({ className }: { className?: string }) {
  const { schema, text } = useProductSchema();
  const options = localeOptions(schema);
  if (options.length < 2) return null;
  return (
    <OptionSelect
      size="sm"
      className={className}
      aria-label={text('ui.common.language')}
      value={activeLocale(schema)}
      options={options.map((option) => ({ value: option.id, label: <span lang={option.id}>{option.label}</span> }))}
      onValueChange={(locale) => chooseLocale(schema, locale)}
    />
  );
}
