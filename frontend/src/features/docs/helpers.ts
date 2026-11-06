import type { DocConnection, DocPage } from './types';
export { normalizeGatewayEndpoint as normalizeDocOrigin } from '../../config/gatewayEndpoint';

export const DEFAULT_CONNECTION: DocConnection = {
  origin: 'YOUR_API_ADDRESS',
  model: 'YOUR_MODEL_ID',
};

export function fillSnippet(text: string, connection: DocConnection): string {
  const origin = connection.origin || DEFAULT_CONNECTION.origin;
  const values: Record<string, string> = {
    origin,
    baseUrl: `${origin}/v1`,
    model: connection.model,
  };
  return text.replace(/\{\{(origin|baseUrl|model)\}\}/g, (_, name: string) => values[name]);
}

export function searchDocs(pages: DocPage[], query: string) {
  const terms = query.toLowerCase().trim().split(/\s+/).filter(Boolean);
  if (!terms.length) return [];
  return pages
    .flatMap(page => {
      const titleText = `${page.title} ${page.description} ${page.group}`;
      const fullText = `${titleText} ${JSON.stringify(page.sections)}`.toLowerCase();
      if (!terms.every(term => fullText.includes(term))) return [];
      const section = page.sections.find(item =>
        terms.every(term => JSON.stringify(item).toLowerCase().includes(term)),
      );
      const titleMatch = terms.every(term => titleText.toLowerCase().includes(term));
      return [{ page, section: titleMatch ? undefined : section, score: titleMatch ? 2 : 1 }];
    })
    .sort((a, b) => b.score - a.score)
    .slice(0, 12);
}
