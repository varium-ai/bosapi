export function businessToday(now = new Date()): string {
  return now.toISOString().slice(0, 10);
}

export function defaultUsageRange(now = new Date(), days: 1 | 7 = 1): { from: string; to: string } {
  return {
    from: businessToday(new Date(now.getTime() - (days - 1) * 24 * 60 * 60 * 1000)),
    to: businessToday(now),
  };
}

export function validUserUsageRange(from: string, to: string): boolean {
  const start = Date.parse(`${from}T00:00:00Z`);
  const end = Date.parse(`${to}T00:00:00Z`);
  return (
    /^\d{4}-\d{2}-\d{2}$/.test(from) &&
    /^\d{4}-\d{2}-\d{2}$/.test(to) &&
    Number.isFinite(start) &&
    Number.isFinite(end) &&
    new Date(start).toISOString().slice(0, 10) === from &&
    new Date(end).toISOString().slice(0, 10) === to &&
    end >= start &&
    end - start < 366 * 24 * 60 * 60 * 1000
  );
}
