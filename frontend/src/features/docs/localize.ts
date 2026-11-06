import type { DocBlock, DocPage } from './types';

/** Share executable examples and stable links across all documentation languages. */
export function localizeDocPages(pages: DocPage[], messages: Record<string, string>): DocPage[] {
  const t = (text: string): string => {
    const translated = messages[text];
    if (!translated?.trim()) throw new Error(`Missing documentation translation: ${text}`);
    return translated;
  };
  const block = (value: DocBlock): DocBlock => {
    switch (value.type) {
      case 'paragraph':
        return { ...value, text: t(value.text) };
      case 'note':
        return { ...value, title: t(value.title), text: t(value.text) };
      case 'list':
        return { ...value, items: value.items.map(t) };
      case 'table':
        return { ...value, headers: value.headers.map(t), rows: value.rows.map(row => row.map(t)) };
      case 'code':
        return { ...value, label: t(value.label) };
      case 'tabs':
        return { ...value, tabs: value.tabs.map(tab => ({ ...tab, label: t(tab.label) })) };
      case 'cards':
        return {
          ...value,
          items: value.items.map(item => ({ ...item, title: t(item.title), text: t(item.text) })),
        };
    }
  };
  return pages.map(page => ({
    ...page,
    title: t(page.title),
    description: t(page.description),
    group: t(page.group),
    audience: page.audience ? t(page.audience) : undefined,
    sections: page.sections.map(section => ({
      ...section,
      title: t(section.title),
      blocks: section.blocks.map(block),
    })),
    sources: page.sources?.map(source => ({ ...source, title: t(source.title) })),
  }));
}
