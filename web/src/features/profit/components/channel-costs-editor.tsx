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
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import { getChannelCosts, updateChannelCosts } from '../api'
import { getChannelCostUpdates, getCostKey } from '../lib/costs'
import type { ChannelCost } from '../types'

const CHANNEL_STATUS_ENABLED = 1

/** The upstream cost of every channel and model, edited in place. */
export function ChannelCostsEditor() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [drafts, setDrafts] = useState<Record<string, string>>({})

  const costsQuery = useQuery({
    queryKey: ['profit', 'costs'],
    queryFn: async () => requireServerSuccess(await getChannelCosts()),
  })
  const costs = useMemo(() => costsQuery.data?.data ?? [], [costsQuery.data])
  const updates = useMemo(
    () => getChannelCostUpdates(costs, drafts),
    [costs, drafts]
  )

  const saveMutation = useMutation({
    mutationFn: async () => {
      if (updates === null) throw new Error(t('Enter a cost of 0 or more'))
      return requireServerSuccess(await updateChannelCosts(updates))
    },
    onSuccess: async () => {
      setDrafts({})
      await queryClient.invalidateQueries({ queryKey: ['profit'] })
      toast.success(t('Upstream costs saved'))
    },
    onError: (error) => handleServerError(error, t('Failed to save costs')),
  })

  const columns = useMemo<StaticDataTableColumn<ChannelCost>[]>(
    () => [
      {
        id: 'channel',
        header: t('Channel'),
        cell: (row) => (
          <span className='flex items-center gap-2'>
            <span className='truncate'>{row.channel_name}</span>
            {row.channel_status === CHANNEL_STATUS_ENABLED ? null : (
              <Badge variant='outline'>{t('Disabled')}</Badge>
            )}
          </span>
        ),
      },
      { id: 'model', header: t('Model'), cell: (row) => row.model_name },
      {
        id: 'cost',
        header: t('Upstream cost per image (USD)'),
        cell: (row) => {
          const key = getCostKey(row)
          return (
            <Input
              type='number'
              inputMode='decimal'
              min={0}
              step='0.0001'
              className='h-8 w-36'
              aria-label={t('Upstream cost of {{model}} at {{channel}}', {
                model: row.model_name,
                channel: row.channel_name,
              })}
              placeholder={t('Not set')}
              value={drafts[key] ?? (row.unit_cost?.toString() || '')}
              onChange={(event) =>
                setDrafts((current) => ({
                  ...current,
                  [key]: event.target.value,
                }))
              }
            />
          )
        },
      },
    ],
    [drafts, t]
  )

  return (
    <div className='flex flex-col gap-3'>
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <p className='text-muted-foreground max-w-prose text-sm'>
          {t(
            'What each image costs you at the upstream. A change applies to requests from now on; days already recorded keep their cost.'
          )}
        </p>
        <Button
          onClick={() => saveMutation.mutate()}
          disabled={
            saveMutation.isPending || updates === null || updates.length === 0
          }
        >
          {t('Save costs')}
        </Button>
      </div>
      <StaticDataTable
        columns={columns}
        data={costs}
        getRowKey={getCostKey}
        emptyContent={
          costsQuery.isLoading ? t('Loading...') : t('No channels yet')
        }
      />
    </div>
  )
}
