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
import { api } from '@/lib/api'

import type { EnterpriseMember, EnterpriseSelf, Page } from './types'

type Envelope<T> = {
  success: boolean
  data: T
  code?: string
  message?: string
}

function unwrap<T>(response: Envelope<T>): T {
  if (response.success === false) {
    const error = new Error(
      response.message || 'Enterprise operation failed'
    ) as Error & { code?: string }
    error.code = response.code
    throw error
  }
  return response.data
}

export async function getEnterpriseSelf(): Promise<EnterpriseSelf> {
  const response = await api.get<Envelope<EnterpriseSelf>>(
    '/api/enterprise/self',
    {
      skipBusinessError: true,
      skipErrorHandler: true,
    }
  )
  return unwrap(response.data)
}

export async function getEnterpriseMembers(
  page: number,
  pageSize: number
): Promise<Page<EnterpriseMember>> {
  const response = await api.get<Envelope<Page<EnterpriseMember>>>(
    '/api/enterprise/members',
    {
      params: { p: page, page_size: pageSize },
      skipBusinessError: true,
      skipErrorHandler: true,
    }
  )
  return unwrap(response.data)
}

export async function changeEnterpriseQuota(
  membershipId: number,
  operation: 'allocations' | 'reclaims',
  quota: number,
  idempotencyKey: string
): Promise<unknown> {
  const response = await api.post<Envelope<unknown>>(
    `/api/enterprise/members/${membershipId}/${operation}`,
    { quota },
    {
      headers: { 'Idempotency-Key': idempotencyKey },
      skipBusinessError: true,
      skipErrorHandler: true,
    }
  )
  return unwrap(response.data)
}

export async function changeEnterpriseMemberStatus(
  membershipId: number,
  operation: 'pause' | 'resume' | 'remove'
): Promise<unknown> {
  const response = await api.post<Envelope<unknown>>(
    `/api/enterprise/members/${membershipId}/${operation}`,
    undefined,
    { skipBusinessError: true, skipErrorHandler: true }
  )
  return unwrap(response.data)
}
