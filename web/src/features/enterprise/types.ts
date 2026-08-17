/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
export type EnterpriseSummary = {
  id: number
  name: string
  status: string
}

export type EnterpriseMembership = {
  membership_id: number
  role: string
  status: string
  available_quota: number
  reserved_quota: number
}

export type EnterpriseWallet = {
  available_quota: number
  reserved_quota: number
  anomaly_quota: number
  member_count: number
}

export type EnterpriseSelf = {
  enterprise: EnterpriseSummary
  membership: EnterpriseMembership
  wallet?: EnterpriseWallet
}

export type EnterpriseMember = {
  membership_id: number
  user_id: number
  username: string
  display_name: string
  role: string
  status: string
  joined_at: number
  paused_at: number
  available_quota: number
  reserved_quota: number
}

export type Page<T> = {
  items: T[]
  total: number
  page: number
  page_size: number
}

export type EnterpriseError = Error & { code?: string }

export type EnterpriseLedgerItem = {
  id: number
  kind: string
  amount: number
  enterprise_available_delta: number
  enterprise_reserved_delta: number
  member_available_delta: number
  member_reserved_delta: number
  created_at: number
}

export type EnterpriseUsageItem = {
  id: number
  model_name: string
  funding_source: string
  membership_role_snapshot: number
  state: string
  reserved_quota: number
  settled_quota: number
  refunded_quota: number
  anomaly_quota: number
  created_at: number
  settled_at: number
  refunded_at: number
}
