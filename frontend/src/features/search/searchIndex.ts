import { getNavigationGroups, navigationKeywords } from '../../app/navigation';
import { firstKeyPath, setupActions } from '../onboarding/firstKey';
import { configurationIssues, issueAliases, issueHref, issueText } from '../diagnostics/catalog';

export interface SearchEntry {
  id: string;
  kind: 'page' | 'action' | 'help';
  name: string;
  module: string;
  description: string;
  href: string;
  keywords: readonly string[];
}
export interface DocMetadata {
  href: string;
  name: [string, string];
  description: [string, string];
}
export function buildSearchIndex(
  role: string | undefined,
  language: string,
  t: (key: string) => string,
  docs: DocMetadata[],
  organizationID?: number,
): SearchEntry[] {
  if (!['super_admin', 'org_admin', 'key_viewer'].includes(role || '')) return [];
  const zh = language === 'zh-CN';
  const pages: SearchEntry[] = getNavigationGroups(role).flatMap(group =>
    group.items.map(item => ({
      id: item.path,
      kind: 'page',
      name: t(item.label),
      module: t(group.label),
      description: (navigationKeywords[item.path] || []).slice(0, 3).join(' · '),
      href: item.path,
      keywords: [item.label, ...(navigationKeywords[item.path] || [])],
    })),
  );
  const actions: SearchEntry[] =
    role === 'key_viewer'
      ? []
      : setupActions
          .filter(action => !action.superAdminOnly || role === 'super_admin')
          .map(action => ({
            id: action.action,
            kind: 'action',
            name: action.title[zh ? 1 : 0],
            module: pages.find(page => page.id === action.path)?.name || '',
            description: zh
              ? '打开配置表单，填写后由你提交'
              : 'Open the form; submit when you are ready',
            href: firstKeyPath(action.path, organizationID, action.action),
            keywords: [...action.title, ...action.keywords],
          }));
  const help: SearchEntry[] = docs.map(doc => ({
    id: doc.href,
    kind: 'help',
    name: issueText(doc.name, language),
    module: zh ? '帮助文档' : 'Documentation',
    description: issueText(doc.description, language),
    href: doc.href,
    keywords: [...doc.name, ...doc.description],
  }));
  const errors: SearchEntry[] = configurationIssues.map(item => ({
    id: item.code,
    kind: 'help',
    name: issueText(item.title, language),
    module: zh ? '配置排错' : 'Troubleshooting',
    description: `${item.code} · ${issueText(item.reason, language)}`,
    href: issueHref(item.code),
    keywords: [
      item.code,
      ...item.title,
      ...item.keywords,
      ...Object.keys(issueAliases).filter(code => issueAliases[code] === item.code),
    ],
  }));
  return [...pages, ...actions, ...help, ...errors];
}
const normalize = (value: string) =>
  value
    .normalize('NFKC')
    .toLocaleLowerCase()
    .replace(/[^\p{L}\p{N}]+/gu, '');
export function searchEntries(entries: SearchEntry[], query: string, limit = 24): SearchEntry[] {
  const term = normalize(query.slice(0, 200));
  if (!term) return entries.filter(entry => entry.kind !== 'help').slice(0, 8);
  return entries
    .map((entry, index) => {
      const terms = [entry.name, ...entry.keywords].map(normalize);
      const score = terms.some(value => value === term)
        ? 100
        : normalize(entry.name).startsWith(term)
          ? 80
          : terms.some(value => value.includes(term))
            ? 60
            : normalize(entry.description).includes(term)
              ? 20
              : 0;
      return { entry, score, index };
    })
    .filter(item => item.score)
    .sort((a, b) => b.score - a.score || a.index - b.index)
    .slice(0, limit)
    .map(item => item.entry);
}
