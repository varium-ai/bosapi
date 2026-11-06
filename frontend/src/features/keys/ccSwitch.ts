import { buildClientImportLink, type CodingClientApp, type KeyImportOptions } from './clientImport';

export type CCSwitchApp = CodingClientApp;

export function buildCCSwitchLink(options: KeyImportOptions): string {
  return buildClientImportLink('cc-switch', options);
}
