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
import { useNavigate } from '@tanstack/react-router'
import type { Row } from '@tanstack/react-table'
import {
  Trash2,
  Edit,
  Power,
  PowerOff,
  ExternalLink,
  ArrowRightLeft,
  Copy,
  Link,
  Loader2,
  RotateCcw,
} from 'lucide-react'
import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { DataTableRowActionMenu } from '@/components/data-table/core/row-action-menu'
import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import {
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuShortcut,
} from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import {
  SecureVerificationDialog,
  useSecureVerification,
  type VerificationMethod,
} from '@/features/auth/secure-verification'
import { useChatPresets } from '@/features/chat/hooks/use-chat-presets'
import { resolveChatUrl, type ChatPreset } from '@/features/chat/lib/chat-links'
import { sendToFluent } from '@/features/chat/lib/send-to-fluent'
import { useStatus } from '@/hooks/use-status'
import { clearAuthentication } from '@/lib/auth-session'
import { encodeChannelConnectionInfo } from '@/lib/channel-connection-info'
import { copyToClipboard } from '@/lib/copy-to-clipboard'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { rotatePrimaryApiKey, updateApiKeyStatus } from '../api'
import { API_KEY_STATUS, ERROR_MESSAGES, SUCCESS_MESSAGES } from '../constants'
import { apiKeySchema } from '../types'
import { useApiKeys } from './api-keys-provider'

function getServerAddress(): string {
  try {
    const raw = localStorage.getItem('status')
    if (raw) {
      const status = JSON.parse(raw)
      if (status.server_address) return status.server_address as string
    }
  } catch {
    /* empty */
  }
  return window.location.origin
}

type DataTableRowActionsProps<TData> = {
  row: Row<TData>
}

export function DataTableRowActions<TData>({
  row,
}: DataTableRowActionsProps<TData>) {
  const { t } = useTranslation()
  const { status } = useStatus()
  const navigate = useNavigate()
  const user = useAuthStore((state) => state.auth.user)
  const apiKey = apiKeySchema.parse(row.original)
  const {
    setOpen,
    setCurrentRow,
    triggerRefresh,
    setResolvedKey,
    resolveRealKey,
    resolvedKeys,
    loadingKeys,
  } = useApiKeys()
  const isEnabled = apiKey.status === API_KEY_STATUS.ENABLED
  const { chatPresets, serverAddress } = useChatPresets()
  const [isTogglingStatus, setIsTogglingStatus] = useState(false)
  const [newKey, setNewKey] = useState<string | null>(null)
  const verification = useSecureVerification()
  const resolvedRealKey = resolvedKeys[apiKey.id]
  const isRealKeyLoading = Boolean(loadingKeys[apiKey.id])

  const hasChatPresets = chatPresets.length > 0
  const toggleLabel = isEnabled ? t('Disable') : t('Enable')
  const isPrivilegedUser =
    user?.role === ROLE.ADMIN || user?.role === ROLE.SUPER_ADMIN
  const canRotate =
    status?.single_primary_api_key_enabled === true && isPrivilegedUser

  const handleMenuOpenChange = useCallback(
    (open: boolean) => {
      if (open && !resolvedRealKey && !isRealKeyLoading) {
        void resolveRealKey(apiKey.id)
      }
    },
    [apiKey.id, isRealKeyLoading, resolvedRealKey, resolveRealKey]
  )

  const getCachedRealKey = useCallback(() => {
    if (resolvedRealKey) return resolvedRealKey
    void resolveRealKey(apiKey.id)
    toast.info(t('API key is loading, please try again in a moment'))
    return null
  }, [apiKey.id, resolvedRealKey, resolveRealKey, t])

  const handleOpenChatPreset = useCallback(
    async (preset: ChatPreset) => {
      const realKey = await resolveRealKey(apiKey.id)
      if (!realKey) return

      if (preset.type === 'fluent') {
        const success = sendToFluent(realKey, serverAddress)
        if (success) {
          toast.success(t('Sent the API key to FluentRead.'))
        } else {
          toast.info(
            t(
              'FluentRead extension not detected. Please ensure it is installed and active.'
            )
          )
        }
        return
      }

      const resolvedUrl = resolveChatUrl({
        template: preset.url,
        apiKey: realKey,
        serverAddress,
      })

      if (!resolvedUrl) {
        toast.error(t('Invalid chat link. Please contact your administrator.'))
        return
      }

      if (typeof window === 'undefined') return

      try {
        window.open(resolvedUrl, '_blank', 'noopener')
      } catch {
        window.location.href = resolvedUrl
      }
    },
    [resolveRealKey, apiKey.id, serverAddress, t]
  )

  const handleToggleStatus = async (
    e?: React.MouseEvent<HTMLButtonElement>
  ) => {
    e?.stopPropagation()
    const newStatus = isEnabled
      ? API_KEY_STATUS.DISABLED
      : API_KEY_STATUS.ENABLED

    setIsTogglingStatus(true)
    try {
      const result = await updateApiKeyStatus(apiKey.id, newStatus)
      if (result.success) {
        const message = isEnabled
          ? t(SUCCESS_MESSAGES.API_KEY_DISABLED)
          : t(SUCCESS_MESSAGES.API_KEY_ENABLED)
        toast.success(message)
        triggerRefresh()
      } else {
        toast.error(result.message || t(ERROR_MESSAGES.STATUS_UPDATE_FAILED))
      }
    } catch {
      toast.error(t(ERROR_MESSAGES.UNEXPECTED))
    } finally {
      setIsTogglingStatus(false)
    }
  }

  const handleRotate = async () => {
    try {
      await verification.startVerification(
        async (proofToken) => {
          if (!proofToken) {
            throw new Error(t('Verification proof was not returned'))
          }
          const response = await rotatePrimaryApiKey(proofToken, apiKey.id)
          if (!response.success) {
            throw new Error(response.message || t('Failed to reset API key'))
          }
          return response
        },
        {
          scope: 'primary_api_key.rotate',
          title: t('Verify before resetting API key'),
          description: t(
            'Confirm your identity with Two-factor Authentication or Passkey before rotating this key.'
          ),
        }
      )
    } catch {
      toast.error(t('Failed to reset API key'))
    }
  }

  const handleVerification = async (
    method: VerificationMethod,
    code?: string
  ) => {
    try {
      const result = await verification.executeVerification(method, code)
      const response = result as {
        success?: boolean
        data?: { full_key?: string; key?: string }
      }
      const rotatedKey = response?.data?.full_key || response?.data?.key
      if (!rotatedKey) throw new Error(t('API Key was not returned'))
      setNewKey(rotatedKey.startsWith('sk-') ? rotatedKey : `sk-${rotatedKey}`)
      triggerRefresh()
    } catch {
      // The verification hook already displays the actionable error.
    }
  }

  const finishRotation = () => {
    setNewKey(null)
    clearAuthentication()
    void navigate({ to: '/sign-in', replace: true })
  }

  let statusIcon = <Power className='size-4' />
  if (isTogglingStatus) {
    statusIcon = <Loader2 className='size-4 animate-spin' />
  } else if (isEnabled) {
    statusIcon = <PowerOff className='size-4' />
  }

  return (
    <>
      <div className='-ml-1.5 flex items-center gap-1'>
        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant='ghost'
                size='icon-sm'
                onClick={handleToggleStatus}
                disabled={isTogglingStatus}
                aria-label={toggleLabel}
                className={
                  isEnabled
                    ? 'text-destructive hover:text-destructive'
                    : 'text-emerald-600 hover:text-emerald-600 dark:text-emerald-400 dark:hover:text-emerald-400'
                }
              />
            }
          >
            {statusIcon}
          </TooltipTrigger>
          <TooltipContent>{toggleLabel}</TooltipContent>
        </Tooltip>

        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant='ghost'
                size='icon-sm'
                onClick={() => {
                  setCurrentRow(apiKey)
                  setOpen('update')
                }}
                aria-label={t('Edit')}
              />
            }
          >
            <Edit />
          </TooltipTrigger>
          <TooltipContent>{t('Edit')}</TooltipContent>
        </Tooltip>

        <DataTableRowActionMenu
          ariaLabel={t('Open menu')}
          contentClassName='w-[200px]'
          modal={false}
          onOpenChange={handleMenuOpenChange}
        >
          <DropdownMenuItem
            onClick={async () => {
              const realKey = getCachedRealKey()
              if (!realKey) return
              const ok = await copyToClipboard(realKey)
              if (ok) toast.success(t('Copied'))
            }}
          >
            {t('Copy Key')}
            <DropdownMenuShortcut>
              <Copy size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={async () => {
              const realKey = getCachedRealKey()
              if (!realKey) return
              const connStr = encodeChannelConnectionInfo(
                realKey,
                getServerAddress()
              )
              const ok = await copyToClipboard(connStr)
              if (ok) toast.success(t('Copied'))
            }}
          >
            {t('Copy Connection Info')}
            <DropdownMenuShortcut>
              <Link size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            onClick={async () => {
              const realKey = await resolveRealKey(apiKey.id)
              if (!realKey) return
              setResolvedKey(realKey)
              setCurrentRow(apiKey)
              setOpen('cc-switch')
            }}
          >
            {t('CC Switch')}
            <DropdownMenuShortcut>
              <ArrowRightLeft size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>
          {hasChatPresets && (
            <DropdownMenuSub>
              <DropdownMenuSubTrigger>{t('Chat')}</DropdownMenuSubTrigger>
              <DropdownMenuSubContent>
                {chatPresets.map((preset) => (
                  <DropdownMenuItem
                    key={preset.id}
                    onClick={() => handleOpenChatPreset(preset)}
                  >
                    {preset.name}
                    {preset.type !== 'web' && (
                      <DropdownMenuShortcut>
                        <ExternalLink size={16} />
                      </DropdownMenuShortcut>
                    )}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuSubContent>
            </DropdownMenuSub>
          )}
          <DropdownMenuSeparator />
          {canRotate && (
            <DropdownMenuItem
              onClick={() => void handleRotate()}
              disabled={verification.open}
            >
              {t('Reset this API key')}
              <DropdownMenuShortcut>
                <RotateCcw size={16} />
              </DropdownMenuShortcut>
            </DropdownMenuItem>
          )}
          <DropdownMenuItem
            onClick={() => {
              setCurrentRow(apiKey)
              setOpen('delete')
            }}
            className='text-destructive focus:text-destructive'
          >
            {t('Delete')}
            <DropdownMenuShortcut>
              <Trash2 size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>
        </DataTableRowActionMenu>
      </div>

      <Dialog
        open={newKey !== null}
        onOpenChange={() => undefined}
        title={t('Save your new API key')}
        description={t(
          'The old key is already invalid. Copy this key now; it will not be shown again.'
        )}
        showCloseButton={false}
        contentClassName='sm:max-w-md'
        footer={
          <Button onClick={finishRotation} className='w-full'>
            {t('I saved it — sign in again')}
          </Button>
        }
      >
        <div className='flex gap-2'>
          <Input
            value={newKey ?? ''}
            readOnly
            className='font-mono text-xs'
            autoFocus
          />
          {newKey && <CopyButton value={newKey} tooltip={t('Copy API key')} />}
        </div>
      </Dialog>

      <SecureVerificationDialog
        open={verification.open}
        onOpenChange={verification.setOpen}
        methods={verification.methods}
        state={verification.state}
        onVerify={handleVerification}
        onCancel={verification.cancel}
        onCodeChange={verification.setCode}
        onMethodChange={verification.switchMethod}
      />
    </>
  )
}
