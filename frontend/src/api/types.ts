export interface APIResponse<T> {
  data: T;
}

export interface PaginatedResult<T> {
  items: T[];
  total: number;
}

export interface OrganizationOption {
  id: number;
  name: string;
}
