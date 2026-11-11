export interface User {
  id: string
  username: string
  email: string
  role: 'PLATFORM_ADMIN' | 'USER' | string
  is_platform_admin: boolean
  is_leader?: boolean
}

export interface GroupSummary {
  id: string
  name: string
  is_system: boolean
  is_leader?: boolean
}

export interface DatabaseAccessSummary {
  database_id: string
  database_name: string
  effective_access: 'VIEW_ONLY' | 'VIEW_EDIT' | string
}

export interface AuthContextType {
  user: User | null
  groups: GroupSummary[]
  databases: DatabaseAccessSummary[]
  activeDBID: string
  setActiveDBID: (id: string) => void
  loading: boolean
  csrfToken: string
  login: (data: any) => Promise<any>
  signup: (data: any) => Promise<any>
  logout: () => Promise<void>
  refreshAuth: () => Promise<void>
}

export interface TableSummary {
  name: string
  schema: string
  is_selected?: boolean
}

export interface ColumnInfo {
  name: string
  data_type: string
  is_nullable: boolean
  is_pk: boolean
  default_value: string
}

export interface ConstraintInfo {
  name: string
  type: string
  definition: string
}

export interface IndexInfo {
  name: string
  columns: string
  is_unique: boolean
  index_type: string
  definition: string
}

export interface TableDataResult {
  database_id: string
  schema_name: string
  table_name: string
  columns: string[]
  rows: string[][]
  total_rows: number
  current_page: number
  total_pages: number
  columns_meta?: ColumnInfo[]
  constraints?: ConstraintInfo[]
  indexes?: IndexInfo[]
  executed_query?: string
  query_error?: string
  truncated?: boolean
  is_query_result?: boolean
  has_schema?: boolean
}

export interface MigrationApproval {
  id?: string
  migration_id: string
  group_id: string
  group_name: string
  leader_id?: string
  leader_name?: string
  status: 'PENDING' | 'APPROVED' | 'REJECTED' | string
  reviewed_at?: string
  rejection_reason?: string
  note?: string
}

export interface MigrationView {
  id: string
  database_id: string
  database_name: string
  version: number
  name: string
  sql: string
  sql_up?: string
  sql_down?: string
  is_irreversible?: boolean
  irreversible_reason?: string
  execution_type?: 'UP' | 'ROLLBACK' | 'FORWARD' | string
  execution_id?: string
  rollback_target_id?: string
  source_migration_id?: string
  can_rollback?: boolean
  can_forward?: boolean
  execution_mode?: 'STANDARD' | 'BATCH' | string
  batch_attribute?: string
  batch_size?: number
  batch_committed_count?: number
  batch_total_count?: number
  checkpoint_cursor?: string | number
  current_phase?: string
  batch_error?: string
  status: 'DRAFT' | 'PENDING_REVIEW' | 'APPROVED' | 'RUNNING' | 'QUEUED' | 'SUCCESS' | 'FAILED' | 'PARTIAL' | 'REJECTED' | 'ABORTED' | 'ROLLED_BACK' | string
  review_required?: boolean
  can_approve?: boolean
  rejection_reason?: string
  approvals?: MigrationApproval[]
  created_by_name: string
  created_at: string
  updated_at: string
  executed_by_name: string
  executed_at: string
  duration_ms: number
  error: string
}

export interface DryRunPlanNodeView {
  depth: number
  title: string
  node_type: string
  join_type?: string
  est_rows: number
  actual_rows: number
  loops: number
  startup_cost: number
  total_cost: number
  actual_ms: number
  conditions?: string[]
  rows_removed: number
  shared_hit: number
  shared_read: number
  never_executed: boolean
  misestimated: boolean
}

export interface DryRunStepView {
  index: number
  sql: string
  duration_ms: number
  kind: 'EXPLAINED' | 'EXECUTED' | 'FAILED' | 'SKIPPED' | string
  planning_ms?: number
  execution_ms?: number
  nodes?: DryRunPlanNodeView[]
}

export interface DryRunView {
  ok: boolean
  statements: number
  duration_ms: number
  error?: string
  steps: DryRunStepView[]
}

export interface UserManagementItem {
  id: string
  username: string
  email: string
  role: string
  is_active: boolean
  groups: GroupSummary[]
}

export interface MemberItem {
  id: string
  username: string
  is_leader?: boolean
}

export interface GroupItem {
  id: string
  name: string
  description: string
  is_system: boolean
  leader_id?: string
  leader_name?: string
  member_count: number
  members: MemberItem[]
  grants: Record<string, string>
}

export interface TargetItem {
  id: string
  name: string
  host: string
  port: number
  db_name: string
  username: string
  ssl_mode: string
  status: 'UNKNOWN' | 'CONNECTED' | 'ERROR' | string
  last_error?: string
  last_checked_at?: string
}

export interface InviteCodeItem {
  code: string
  group_id: string
  group_name: string
  is_active: boolean
  created_at: string
}

export interface AdminOverview {
  users: UserManagementItem[]
  groups: GroupItem[]
  targets: TargetItem[]
  codes: InviteCodeItem[]
}
