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
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { Badge } from '@/components/ui/badge'
import { Switch } from '@/components/ui/switch'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatQuota } from '@/lib/format'
import { cn } from '@/lib/utils'

import { getProfitGroupColumnTitle, getProfitRowLabel } from '../lib/report'
import type { ProfitGroupBy, ProfitReportRow } from '../types'

type ProfitReportTableProps = {
  rows: ProfitReportRow[]
  groupBy: ProfitGroupBy
  /** Id of the key whose statistics switch is being saved. */
  pendingTokenId?: number
  onCountedChange?: (tokenId: number, counted: boolean) => void
}

const NUMBER_CELL = 'text-right tabular-nums'

function getRowKey(row: ProfitReportRow, index: number): string {
  return String(
    row.stat_day ?? row.token_id ?? row.channel_id ?? row.model_name ?? index
  )
}

/** Profit rows of one grouping; the key grouping can switch a key's statistics. */
export function ProfitReportTable(props: ProfitReportTableProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const { groupBy, pendingTokenId, onCountedChange } = props

  const columns = useMemo(() => {
    const result: StaticDataTableColumn<ProfitReportRow>[] = [
      {
        id: 'group',
        header: getProfitGroupColumnTitle(groupBy, t),
        cell: (row) => (
          <span className='flex items-center gap-2'>
            <span className='truncate'>
              {getProfitRowLabel(row, groupBy, t)}
            </span>
            {row.token_deleted ? (
              <Badge variant='outline'>{t('Key deleted')}</Badge>
            ) : null}
          </span>
        ),
      },
    ]
    if (groupBy === 'token') {
      result.push({
        id: 'counted',
        header: t('Counted in profit'),
        cell: (row) => {
          const tokenId = row.token_id
          if (!tokenId || row.token_deleted) return null
          return (
            <Switch
              size='sm'
              aria-label={t('Counted in profit')}
              checked={!row.profit_excluded}
              disabled={pendingTokenId === tokenId}
              onCheckedChange={(checked) => onCountedChange?.(tokenId, checked)}
            />
          )
        },
      })
    }
    result.push(
      {
        id: 'requests',
        header: t('Requests'),
        className: 'text-right',
        cellClassName: NUMBER_CELL,
        cell: (row) => formatNumber(row.request_count, locale),
      },
      {
        id: 'images',
        header: t('Images'),
        className: 'text-right',
        cellClassName: NUMBER_CELL,
        cell: (row) => formatNumber(row.image_count, locale),
      },
      {
        id: 'revenue',
        header: t('Revenue'),
        className: 'text-right',
        cellClassName: NUMBER_CELL,
        cell: (row) => formatQuota(row.revenue),
      },
      {
        id: 'cost',
        header: t('Upstream cost'),
        className: 'text-right',
        cellClassName: NUMBER_CELL,
        cell: (row) => (
          <span className='inline-flex items-center justify-end gap-2'>
            {row.uncosted_count > 0 ? (
              <Badge variant='outline' className='text-warning'>
                {t('{{count}} without a cost', { count: row.uncosted_count })}
              </Badge>
            ) : null}
            {formatQuota(row.cost)}
          </span>
        ),
      },
      {
        id: 'profit',
        header: t('Profit'),
        className: 'text-right',
        cellClassName: (row) =>
          cn(
            NUMBER_CELL,
            'font-semibold',
            row.profit < 0 && 'text-destructive'
          ),
        cell: (row) => formatQuota(row.profit),
      }
    )
    return result
  }, [groupBy, locale, onCountedChange, pendingTokenId, t])

  return (
    <StaticDataTable
      columns={columns}
      data={props.rows}
      getRowKey={getRowKey}
      getRowClassName={(row) =>
        row.profit_excluded ? 'text-muted-foreground' : undefined
      }
      emptyContent={t('No profit recorded in this period')}
    />
  )
}
