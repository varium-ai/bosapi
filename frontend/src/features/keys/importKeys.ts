export interface ImportedAPIKey {
  name: string;
  api_key: string;
}

const keyHeaders = new Set([
  'api_key',
  'apikey',
  'key',
  'key_value',
  'keyvalue',
  'token',
  'access_token',
  'secret',
  'secret_key',
  'credential',
  'value',
  '密钥',
  '密鑰',
  '密钥值',
  '密鑰值',
  '令牌',
  '访问令牌',
  '存取權杖',
]);
const nameHeaders = new Set([
  'name',
  'key_name',
  'keyname',
  'label',
  'title',
  '名称',
  '名字',
  '密钥名称',
  '密鑰名稱',
]);
const ignoredSingleCellRows = new Set([
  'api_key',
  'api_keys',
  'api_key_export',
  'api_keys_export',
  'key_export',
  'keys_export',
  '密钥列表',
  '密鑰列表',
  'api密钥导出',
  'api密鑰匯出',
]);

export async function parseAPIKeyImportFile(file: File): Promise<ImportedAPIKey[]> {
  if (file.size > 10 * 1024 * 1024) throw new Error('File must be smaller than 10 MB');
  const extension = file.name.split('.').pop()?.toLowerCase();
  let rows: unknown[][];
  if (extension === 'csv') {
    rows = parseCSV(await file.text());
  } else if (extension === 'xlsx' || extension === 'xls') {
    const XLSX = await import('xlsx');
    const workbook = XLSX.read(await file.arrayBuffer(), { type: 'array', raw: false });
    const firstSheet = workbook.Sheets[workbook.SheetNames[0]];
    if (!firstSheet) throw new Error('The spreadsheet has no worksheets');
    rows = XLSX.utils.sheet_to_json(firstSheet, {
      header: 1,
      raw: false,
      defval: '',
    }) as unknown[][];
  } else {
    throw new Error('Choose a CSV, XLS, or XLSX file');
  }
  if (rows.length > 5000) throw new Error('The file has too many rows to inspect');
  return identifyRows(rows);
}

function identifyRows(rows: unknown[][]): ImportedAPIKey[] {
  const normalizedRows = rows.map(row =>
    row.map(cell =>
      String(cell ?? '')
        .replace(/^\uFEFF/, '')
        .trim(),
    ),
  );
  const firstDataIndex = normalizedRows.findIndex(row => row.some(Boolean));
  if (firstDataIndex < 0) throw new Error('The file contains no data');

  const headerIndex = normalizedRows.findIndex(
    (row, index) =>
      index >= firstDataIndex &&
      index < firstDataIndex + 10 &&
      row.some(cell => keyHeaders.has(normalizeHeader(cell))),
  );
  const header = headerIndex >= 0 ? normalizedRows[headerIndex] : [];
  const keyIndex = header.findIndex(cell => keyHeaders.has(normalizeHeader(cell)));
  const nameIndex = header.findIndex(cell => nameHeaders.has(normalizeHeader(cell)));
  const hasHeader = headerIndex >= 0;
  const dataRows = normalizedRows.slice(hasHeader ? headerIndex + 1 : firstDataIndex);
  const imported: ImportedAPIKey[] = [];

  for (const row of dataRows) {
    const cells = row.map((value, index) => ({ value, index })).filter(cell => cell.value !== '');
    if (!cells.length) continue;

    let apiKey = '';
    let name = '';
    if (hasHeader) {
      apiKey = row[keyIndex] || '';
      name = nameIndex >= 0 ? row[nameIndex] || '' : '';
    } else {
      const keyCandidates = cells.filter(({ value }) => value.length >= 8 && !/\s/.test(value));
      const keyCell = keyCandidates.slice().sort((a, b) => b.value.length - a.value.length)[0];
      if (!keyCell) continue;
      if (cells.length === 1 && ignoredSingleCellRows.has(normalizeHeader(keyCell.value))) continue;
      apiKey = keyCell.value;
      const nameCandidates = cells
        .filter(({ value, index }) => index !== keyCell.index && !/^\d+$/.test(value))
        .sort((a, b) => b.value.length - a.value.length);
      name = nameCandidates[0]?.value || '';
    }

    apiKey = apiKey.trim();
    name = name.trim();
    if (!apiKey) continue;
    if (new TextEncoder().encode(apiKey).byteLength > 4096 || /[\r\n]/.test(apiKey))
      throw new Error(`An API key on row ${imported.length + 1} is invalid`);
    if (Array.from(name).length > 100)
      throw new Error(`A key name on row ${imported.length + 1} is longer than 100 characters`);
    imported.push({ name, api_key: apiKey });
    if (imported.length > 500) throw new Error('Import supports up to 500 API keys at a time');
  }

  if (!imported.length)
    throw new Error(
      'No API keys were recognized. Include a key column or a value at least 8 characters long.',
    );
  return imported;
}

function normalizeHeader(value: string): string {
  return value.toLowerCase().replace(/[\s-]+/g, '_');
}

function parseCSV(source: string): string[][] {
  const text = source.replace(/^\uFEFF/, '');
  const sample = text.split(/\r?\n/).slice(0, 10);
  const delimiters = [',', '\t', ';'];
  const delimiter =
    delimiters
      .map(candidate => ({
        candidate,
        count: sample.reduce((total, line) => total + countUnquoted(line, candidate), 0),
      }))
      .sort((a, b) => b.count - a.count)[0]?.candidate || ',';

  const rows: string[][] = [];
  let row: string[] = [];
  let field = '';
  let quoted = false;
  for (let index = 0; index < text.length; index += 1) {
    const char = text[index];
    if (char === '"') {
      if (quoted && text[index + 1] === '"') {
        field += '"';
        index += 1;
      } else {
        quoted = !quoted;
      }
    } else if (!quoted && char === delimiter) {
      row.push(field);
      field = '';
    } else if (!quoted && (char === '\n' || char === '\r')) {
      if (char === '\r' && text[index + 1] === '\n') index += 1;
      row.push(field);
      rows.push(row);
      row = [];
      field = '';
    } else {
      field += char;
    }
  }
  row.push(field);
  rows.push(row);
  return rows;
}

function countUnquoted(value: string, delimiter: string): number {
  let quoted = false;
  let count = 0;
  for (let index = 0; index < value.length; index += 1) {
    if (value[index] === '"') {
      if (quoted && value[index + 1] === '"') index += 1;
      else quoted = !quoted;
    } else if (!quoted && value[index] === delimiter) {
      count += 1;
    }
  }
  return count;
}
