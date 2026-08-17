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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ChevronLeft, ChevronRight, Loader2, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { SectionPageLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { formatQuota } from '@/lib/format'

import {
  changeEnterpriseMemberStatus,
  changeEnterpriseQuota,
  getEnterpriseMembers,
  getEnterpriseSelf,
} from './api'
import { EnterpriseReadPanels } from './ledger-usage'
import type { EnterpriseError, EnterpriseMember } from './types'

const queryKey = ['enterprise']
const memberPageSize = 20

function newIdempotencyKey(): string {
  return (
    globalThis.crypto?.randomUUID?.() ||
    `${Date.now()}-${Math.random().toString(36).slice(2)}`
  )
}

function errorCode(error: unknown): string | undefined {
  const candidate = error as EnterpriseError & {
    response?: { data?: { code?: string } }
  }
  return candidate?.code || candidate?.response?.data?.code
}

function errorMessage(error: unknown, t: (key: string) => string): string {
  const code = errorCode(error)
  if (code === 'ENTERPRISE_FEATURE_DISABLED') {
    return t('Enterprise management is not enabled.')
  }
  if (code === 'ENTERPRISE_MEMBERSHIP_REQUIRED') {
    return t('You do not belong to an enterprise.')
  }
  if (code === 'ENTERPRISE_OWNER_REQUIRED') {
    return t('Only the enterprise owner can manage members.')
  }
  if (code === 'ENTERPRISE_INSUFFICIENT_QUOTA') {
    return t('The enterprise wallet does not have enough available quota.')
  }
  if (code === 'ENTERPRISE_REMOVAL_PENDING') {
    return t('This member is still being drained; removal is not complete.')
  }
  if (code === 'ENTERPRISE_INVALID_MEMBER_STATE') {
    return t('This member cannot perform that action in its current state.')
  }
  return t('Enterprise operation failed. Please refresh and try again.')
}

function statusVariant(status: string) {
  if (status === 'ACTIVE') return 'default' as const
  if (status === 'PAUSED' || status === 'DRAINING') return 'warning' as const
  return 'outline' as const
}

type MemberActionProps = {
  member: EnterpriseMember
  disabled: boolean
  onDone: () => void
  onFeatureDisabled: () => void
}

function MemberActions(props: MemberActionProps) {
  const { t } = useTranslation()
  const [quota, setQuota] = useState('')
  const [quotaMode, setQuotaMode] = useState<
    'allocations' | 'reclaims' | null
  >(null)
  const [confirm, setConfirm] = useState<
    'pause' | 'resume' | 'remove' | null
  >(null)
  const [idempotencyKey, setIdempotencyKey] = useState<string | null>(null)
  const queryClient = useQueryClient()
  const statusMutation = useMutation({
    mutationFn: (operation: 'pause' | 'resume' | 'remove') =>
      changeEnterpriseMemberStatus(props.member.membership_id, operation),
    onSuccess: () => {
      setConfirm(null)
      void queryClient.invalidateQueries({ queryKey })
      props.onDone()
    },
    onError: (error) => {
      if (errorCode(error) === 'ENTERPRISE_FEATURE_DISABLED') {
        props.onFeatureDisabled()
      }
      toast.error(errorMessage(error, t))
    },
  })
  const quotaMutation = useMutation({
    mutationFn: ({
      amount,
      operation,
      key,
    }: {
      amount: number
      operation: 'allocations' | 'reclaims'
      key: string
    }) =>
      changeEnterpriseQuota(
        props.member.membership_id,
        operation,
        amount,
        key
      ),
    onSuccess: () => {
      setQuota('')
      setQuotaMode(null)
      setIdempotencyKey(null)
      void queryClient.invalidateQueries({ queryKey })
      props.onDone()
    },
    onError: (error) => {
      if (errorCode(error) === 'ENTERPRISE_FEATURE_DISABLED') {
        props.onFeatureDisabled()
      }
      toast.error(errorMessage(error, t))
    },
  })

  const submitQuota = (): void => {
    const amount = Number(quota)
    if (!quotaMode || !Number.isSafeInteger(amount) || amount <= 0) {
      toast.error(t('Quota must be a positive whole number.'))
      return
    }

    const key = idempotencyKey || newIdempotencyKey()
    setIdempotencyKey(key)
    quotaMutation.mutate({ amount, operation: quotaMode, key })
  }

  const memberName = props.member.display_name || props.member.username

  return (
    <div className='flex min-w-55 flex-wrap items-center justify-end gap-1.5'>
      {quotaMode ? (
        <div className='flex w-full items-center gap-1.5 sm:w-auto'>
          <label
            className='sr-only'
            htmlFor={`quota-${props.member.membership_id}`}
          >
            {t('Quota amount')}
          </label>
          <Input
            id={`quota-${props.member.membership_id}`}
            type='number'
            min={1}
            value={quota}
            disabled={quotaMutation.isPending}
            onChange={(event) => setQuota(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter') submitQuota()
            }}
            placeholder={t('Amount')}
            className='w-24'
          />
          <Button
            size='sm'
            onClick={submitQuota}
            disabled={quotaMutation.isPending}
          >
            {quotaMutation.isPending ? (
              <Loader2 className='animate-spin' />
            ) : (
              t('Confirm')
            )}
          </Button>
          <Button
            size='sm'
            variant='ghost'
            onClick={() => {
              setQuotaMode(null)
              setIdempotencyKey(null)
            }}
            disabled={quotaMutation.isPending}
          >
            {t('Cancel')}
          </Button>
        </div>
      ) : (
        <>
          <Button
            size='sm'
            variant='outline'
            onClick={() => setQuotaMode('allocations')}
            disabled={props.disabled || props.member.status !== 'ACTIVE'}
          >
            {t('Allocate')}
          </Button>
          <Button
            size='sm'
            variant='outline'
            onClick={() => setQuotaMode('reclaims')}
            disabled={
              props.disabled ||
              !['ACTIVE', 'PAUSED'].includes(props.member.status)
            }
          >
            {t('Reclaim')}
          </Button>
          <Button
            size='sm'
            variant='outline'
            onClick={() =>
              setConfirm(props.member.status === 'PAUSED' ? 'resume' : 'pause')
            }
            disabled={
              props.disabled ||
              !['ACTIVE', 'PAUSED'].includes(props.member.status)
            }
          >
            {props.member.status === 'PAUSED' ? t('Resume') : t('Pause')}
          </Button>
          <Button
            size='sm'
            variant='destructive'
            onClick={() => setConfirm('remove')}
            disabled={
              props.disabled ||
              !['ACTIVE', 'PAUSED', 'DRAINING'].includes(props.member.status)
            }
          >
            {props.member.status === 'DRAINING'
              ? t('Check and complete removal')
              : t('Remove')}
          </Button>
        </>
      )}
      <ConfirmDialog
        open={confirm !== null}
        onOpenChange={(open) => !open && setConfirm(null)}
        title={t('Confirm member action')}
        desc={
          confirm === 'remove'
            ? t(
                'Remove {{name}}? Any remaining quota must be drained first. The member may remain in DRAINING state.',
                { name: memberName }
              )
            : t('{{action}} {{name}}?', {
                action: confirm === 'pause' ? t('Pause') : t('Resume'),
                name: memberName,
              })
        }
        destructive={confirm === 'remove'}
        isLoading={statusMutation.isPending}
        handleConfirm={() => confirm && statusMutation.mutate(confirm)}
        confirmText={t('Confirm')}
      />
    </div>
  )
}

function EnterpriseContent() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [memberPage, setMemberPage] = useState(1)
  const [featureDisabled, setFeatureDisabled] = useState(false)
  const self = useQuery({
    queryKey: [...queryKey, 'self'],
    queryFn: getEnterpriseSelf,
    enabled: !featureDisabled,
  })
  const members = useQuery({
    queryKey: [...queryKey, 'members', memberPage],
    queryFn: () => getEnterpriseMembers(memberPage, memberPageSize),
    enabled: self.data?.membership.role === 'OWNER' && !featureDisabled,
  })

  const refresh = (): void => {
    setFeatureDisabled(false)
    void queryClient.resetQueries({ queryKey })
  }
  const closeFeature = (): void => {
    setFeatureDisabled(true)
    queryClient.removeQueries({ queryKey })
  }

  if (
    featureDisabled ||
    errorCode(self.error) === 'ENTERPRISE_FEATURE_DISABLED'
  ) {
    return (
      <Card>
        <CardContent className='flex flex-wrap items-center justify-between gap-3 pt-4'>
          <p>{t('Enterprise management is not enabled.')}</p>
          <Button variant='outline' onClick={refresh}>
            <RefreshCw />
            {t('Refresh')}
          </Button>
        </CardContent>
      </Card>
    )
  }

  if (self.isLoading) {
    return (
      <div className='text-muted-foreground flex items-center gap-2'>
        <Loader2 className='animate-spin' />
        {t('Loading...')}
      </div>
    )
  }
  if (self.error) {
    return (
      <Card>
        <CardContent className='flex flex-wrap items-center justify-between gap-3 pt-4'>
          <p>{errorMessage(self.error, t)}</p>
          <Button variant='outline' onClick={refresh}>
            <RefreshCw />
            {t('Refresh')}
          </Button>
        </CardContent>
      </Card>
    )
  }
  if (!self.data) return null

  const summary = self.data
  const isOwner = summary.membership.role === 'OWNER'
  const totalPages = Math.max(
    1,
    Math.ceil((members.data?.total ?? 0) / memberPageSize)
  )

  return (
    <div className='space-y-4'>
      <Card>
        <CardHeader>
          <CardTitle>{summary.enterprise.name}</CardTitle>
          <CardDescription>{t('Enterprise overview')}</CardDescription>
        </CardHeader>
        <CardContent className='grid gap-3 sm:grid-cols-3'>
          <div>
            <p className='text-muted-foreground text-xs'>
              {t('Enterprise status')}
            </p>
            <Badge variant={statusVariant(summary.enterprise.status)}>
              {summary.enterprise.status}
            </Badge>
          </div>
          <div>
            <p className='text-muted-foreground text-xs'>{t('Membership')}</p>
            <p>
              {summary.membership.role} · {summary.membership.status}
            </p>
          </div>
          <div>
            <p className='text-muted-foreground text-xs'>
              {t('Available quota')}
            </p>
            <p>{formatQuota(summary.membership.available_quota)}</p>
            <p className='text-muted-foreground text-xs'>
              {t('Reserved quota')}:{' '}
              {formatQuota(summary.membership.reserved_quota)}
            </p>
          </div>
        </CardContent>
      </Card>

      {isOwner && summary.wallet && (
        <Card>
          <CardHeader>
            <CardTitle>{t('Enterprise wallet')}</CardTitle>
            <CardDescription>
              {t('Only enterprise-funded quota is shown here.')}
            </CardDescription>
          </CardHeader>
          <CardContent className='grid gap-3 sm:grid-cols-3'>
            <div>
              <p className='text-muted-foreground text-xs'>
                {t('Available quota')}
              </p>
              <p>{formatQuota(summary.wallet.available_quota)}</p>
            </div>
            <div>
              <p className='text-muted-foreground text-xs'>
                {t('Reserved quota')}
              </p>
              <p>{formatQuota(summary.wallet.reserved_quota)}</p>
            </div>
            <div>
              <p className='text-muted-foreground text-xs'>{t('Members')}</p>
              <p>{summary.wallet.member_count}</p>
            </div>
          </CardContent>
        </Card>
      )}

      {isOwner && (
        <Card>
          <CardHeader>
            <CardTitle>{t('Members')}</CardTitle>
            <CardDescription>
              {t('Manage enterprise-funded quota and membership status.')}
            </CardDescription>
          </CardHeader>
          <CardContent>
            {members.isLoading ? (
              <div className='text-muted-foreground flex items-center gap-2'>
                <Loader2 className='animate-spin' />
                {t('Loading...')}
              </div>
            ) : members.error ? (
              <div className='flex items-center justify-between gap-3'>
                <p>{errorMessage(members.error, t)}</p>
                <Button variant='outline' onClick={refresh}>
                  {t('Refresh')}
                </Button>
              </div>
            ) : (
              <>
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t('Member')}</TableHead>
                      <TableHead>{t('Status')}</TableHead>
                      <TableHead>{t('Available quota')}</TableHead>
                      <TableHead>{t('Reserved quota')}</TableHead>
                      <TableHead className='text-right'>
                        {t('Actions')}
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {members.data?.items.map((member) => (
                      <TableRow key={member.membership_id}>
                        <TableCell>
                          <div className='font-medium'>
                            {member.display_name || member.username}
                          </div>
                          <div className='text-muted-foreground text-xs'>
                            {member.username}
                          </div>
                        </TableCell>
                        <TableCell>
                          <Badge variant={statusVariant(member.status)}>
                            {member.status}
                          </Badge>
                          {member.status === 'MANUAL_REVIEW' && (
                            <p className='text-muted-foreground mt-1 text-xs'>
                              {t('Manual review is required.')}
                            </p>
                          )}
                        </TableCell>
                        <TableCell>
                          {formatQuota(member.available_quota)}
                        </TableCell>
                        <TableCell>
                          {formatQuota(member.reserved_quota)}
                        </TableCell>
                        <TableCell>
                          {member.role === 'MEMBER' ? (
                            <MemberActions
                              member={member}
                              disabled={members.isFetching || featureDisabled}
                              onDone={refresh}
                              onFeatureDisabled={closeFeature}
                            />
                          ) : (
                            <span className='text-muted-foreground'>—</span>
                          )}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
                {(members.data?.total ?? 0) > memberPageSize && (
                  <div className='mt-4 flex flex-wrap items-center justify-between gap-3'>
                    <p className='text-muted-foreground text-sm'>
                      {t('Page {{current}} of {{total}}', {
                        current: memberPage,
                        total: totalPages,
                      })}
                    </p>
                    <div className='flex gap-2'>
                      <Button
                        size='sm'
                        variant='outline'
                        onClick={() =>
                          setMemberPage((page) => Math.max(1, page - 1))
                        }
                        disabled={memberPage <= 1}
                      >
                        <ChevronLeft />
                        {t('Previous page')}
                      </Button>
                      <Button
                        size='sm'
                        variant='outline'
                        onClick={() => setMemberPage((page) => page + 1)}
                        disabled={memberPage >= totalPages}
                      >
                        {t('Next page')}
                        <ChevronRight />
                      </Button>
                    </div>
                  </div>
                )}
              </>
            )}
          </CardContent>
        </Card>
      )}

      <EnterpriseReadPanels
        isOwner={isOwner}
        featureDisabled={featureDisabled}
        errorText={(error) => errorMessage(error, t)}
        onFeatureDisabled={closeFeature}
      />
    </div>
  )
}

export function Enterprise() {
  const { t } = useTranslation()

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Enterprise')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <EnterpriseContent />
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
