export type DocBlock =
  | { type: 'paragraph'; text: string }
  | { type: 'note'; title: string; text: string; tone?: 'info' | 'warning' }
  | { type: 'list'; items: string[]; ordered?: boolean }
  | { type: 'table'; headers: string[]; rows: string[][] }
  | { type: 'code'; label: string; language: string; code: string }
  | { type: 'tabs'; tabs: { label: string; language: string; code: string }[] }
  | { type: 'cards'; items: { title: string; text: string; href: string; icon?: string }[] };

export interface DocSection {
  id: string;
  title: string;
  blocks: DocBlock[];
}

export interface DocPage {
  slug: string;
  title: string;
  description: string;
  group: string;
  icon: string;
  audience?: string;
  protocol?: 'chat' | 'responses' | 'messages';
  sections: DocSection[];
  sources?: { title: string; url: string }[];
}

export interface DocConnection {
  origin: string;
  model: string;
}
