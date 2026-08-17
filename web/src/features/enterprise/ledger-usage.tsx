/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useQuery } from '@tanstack/react-query'
import { ChevronLeft, ChevronRight, Loader2 } from 'lucide-react'
import type { ReactNode } from 'react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { formatQuota, formatTimestamp } from '@/lib/format'

import { getEnterpriseLedger, getEnterpriseUsage } from './api'
import type {
  EnterpriseError,
  EnterpriseLedgerItem,
  EnterpriseUsageItem,
  Page,
} from './types'

const readPageSize = 20

function getErrorCode(error: unknown): string | undefined {
  const candidate = error as EnterpriseError & {
    response?: { data?: { code?: string } }
  }
  return candidate?.code || candidate?.response?.data?.code
}

type PageControlsProps = {
  page: number
  total: number
  pageSize: number
  onPageChange: (page: number) => void
}

function PageControls(props: PageControlsProps) {
  const { t } = useTranslation()
  const totalPages = Math.max(1, Math.ceil(props.total / props.pageSize))

  if (props.total <= props.pageSize) return null

  return (
    <div className='mt-4 flex flex-wrap items-center justify-between gap-3'>
      <p className='text-muted-foreground text-sm'>
        {t('Page {{current}} of {{total}}', {
          current: props.page,
          total: totalPages,
        })}
      </p>
      <div className='flex gap-2'>
        <Button
          size='sm'
          variant='outline'
          aria-label={t('Previous page')}
          onClick={() => props.onPageChange(Math.max(1, props.page - 1))}
          disabled={props.page <= 1}
        >
          <ChevronLeft aria-hidden='true' />
          {t('Previous page')}
        </Button>
        <Button
          size='sm'
          variant='outline'
          aria-label={t('Next page')}
          onClick={() =>
            props.onPageChange(Math.min(totalPages, props.page + 1))
          }
          disabled={props.page >= totalPages}
        >
          {t('Next page')}
          <ChevronRight aria-hidden='true' />
        </Button>
      </div>
    </div>
  )
}

type ReadCardProps<T> = {
  title: string
  description: string
  query: {
    data?: Page<T>
    isLoading: boolean
    isFetching: boolean
    error: unknown
  }
  emptyText: string
  errorText: string
  children: (items: T[]) => ReactNode
  page: number
  onPageChange: (page: number) => void
  onRefresh: () => void
}

function ReadCard<T>(props: ReadCardProps<T>) {
  const { t } = useTranslation()

  return (
    <Card>
      <CardHeader>
        <CardTitle>{props.title}</CardTitle>
        <CardDescription>{props.description}</CardDescription>
      </CardHeader>
      <CardContent>
        {props.query.isLoading ? (
          <div className='text-muted-foreground flex items-center gap-2'>
            <Loader2 className='animate-spin' aria-hidden='true' />
            {t('Loading...')}
          </div>
        ) : props.query.error ? (
          <div className='flex flex-wrap items-center justify-between gap-3'>
            <p>{props.errorText}</p>
            <Button variant='outline' onClick={props.onRefresh}>
              {t('Refresh')}
            </Button>
          </div>
        ) : props.query.data?.items.length ? (
          <>
            <div className='overflow-x-auto'>
              {props.children(props.query.data.items)}
            </div>
            <PageControls
              page={props.page}
              total={props.query.data.total}
              pageSize={readPageSize}
              onPageChange={props.onPageChange}
            />
            {props.query.isFetching && (
              <p className='text-muted-foreground mt-2 text-xs'>
                {t('Refreshing...')}
              </p>
            )}
          </>
        ) : (
          <p className='text-muted-foreground'>{props.emptyText}</p>
        )}
      </CardContent>
    </Card>
  )
}

function LedgerTable(props: { items: EnterpriseLedgerItem[] }) {
  const { t } = useTranslation()

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>{t('Date')}</TableHead>
          <TableHead>{t('Type')}</TableHead>
          <TableHead>{t('Amount')}</TableHead>
          <TableHead>{t('Enterprise available change')}</TableHead>
          <TableHead>{t('Enterprise reserved change')}</TableHead>
          <TableHead>{t('Member available change')}</TableHead>
          <TableHead>{t('Member reserved change')}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {props.items.map((item) => (
          <TableRow key={item.id}>
            <TableCell className='whitespace-nowrap'>
              {formatTimestamp(item.created_at)}
            </TableCell>
            <TableCell>
              <Badge variant='outline'>{item.kind}</Badge>
            </TableCell>
            <TableCell>{formatQuota(item.amount)}</TableCell>
            <TableCell>{formatQuota(item.enterprise_available_delta)}</TableCell>
            <TableCell>{formatQuota(item.enterprise_reserved_delta)}</TableCell>
            <TableCell>{formatQuota(item.member_available_delta)}</TableCell>
            <TableCell>{formatQuota(item.member_reserved_delta)}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

function UsageTable(props: { items: EnterpriseUsageItem[] }) {
  const { t } = useTranslation()

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>{t('Date')}</TableHead>
          <TableHead>{t('Model')}</TableHead>
          <TableHead>{t('Funding source')}</TableHead>
          <TableHead>{t('State')}</TableHead>
          <TableHead>{t('Reserved quota')}</TableHead>
          <TableHead>{t('Settled quota')}</TableHead>
          <TableHead>{t('Refunded quota')}</TableHead>
          <TableHead>{t('Anomaly quota')}</TableHead>
          <TableHead>{t('Membership role')}</TableHead>
          <TableHead>{t('Settled at')}</TableHead>
          <TableHead>{t('Refunded at')}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {props.items.map((item) => (
          <TableRow key={item.id}>
            <TableCell className='whitespace-nowrap'>
              {formatTimestamp(item.created_at)}
            </TableCell>
            <TableCell>{item.model_name}</TableCell>
            <TableCell>{item.funding_source}</TableCell>
            <TableCell>
              <Badge variant='outline'>{item.state}</Badge>
            </TableCell>
            <TableCell>{formatQuota(item.reserved_quota)}</TableCell>
            <TableCell>{formatQuota(item.settled_quota)}</TableCell>
            <TableCell>{formatQuota(item.refunded_quota)}</TableCell>
            <TableCell>{formatQuota(item.anomaly_quota)}</TableCell>
            <TableCell>{item.membership_role_snapshot}</TableCell>
            <TableCell className='whitespace-nowrap'>
              {item.settled_at ? formatTimestamp(item.settled_at) : '-'}
            </TableCell>
            <TableCell className='whitespace-nowrap'>
              {item.refunded_at ? formatTimestamp(item.refunded_at) : '-'}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

type EnterpriseReadPanelsProps = {
  isOwner: boolean
  featureDisabled: boolean
  errorText: (error: unknown) => string
  onFeatureDisabled: () => void
}

export function EnterpriseReadPanels(props: EnterpriseReadPanelsProps) {
  const { t } = useTranslation()
  const [ledgerPage, setLedgerPage] = useState(1)
  const [usagePage, setUsagePage] = useState(1)
  const ledger = useQuery({
    queryKey: ['enterprise', 'ledger', ledgerPage],
    queryFn: () => getEnterpriseLedger(ledgerPage, readPageSize),
    enabled: props.isOwner && !props.featureDisabled,
  })
  const usage = useQuery({
    queryKey: ['enterprise', 'usage', usagePage],
    queryFn: () => getEnterpriseUsage(usagePage, readPageSize),
    enabled: !props.featureDisabled,
  })

  useEffect(() => {
    if (
      getErrorCode(ledger.error) === 'ENTERPRISE_FEATURE_DISABLED' ||
      getErrorCode(usage.error) === 'ENTERPRISE_FEATURE_DISABLED'
    ) {
      props.onFeatureDisabled()
    }
  }, [ledger.error, props, usage.error])

  const refresh = (): void => {
    void ledger.refetch()
    void usage.refetch()
  }

  return (
    <div className='space-y-4'>
      {props.isOwner && (
        <ReadCard
          title={t('Enterprise ledger')}
          description={t('Read-only history of enterprise quota changes.')}
          query={ledger}
          emptyText={t('No enterprise ledger records.')}
          errorText={props.errorText(ledger.error)}
          page={ledgerPage}
          onPageChange={setLedgerPage}
          onRefresh={refresh}
        >
          {(items) => <LedgerTable items={items} />}
        </ReadCard>
      )}
      <ReadCard
        title={t('Enterprise usage')}
        description={t('Read-only history of enterprise-funded API usage.')}
        query={usage}
        emptyText={t('No enterprise usage records.')}
        errorText={props.errorText(usage.error)}
        page={usagePage}
        onPageChange={setUsagePage}
        onRefresh={refresh}
      >
        {(items) => <UsageTable items={items} />}
      </ReadCard>
    </div>
  )
}
