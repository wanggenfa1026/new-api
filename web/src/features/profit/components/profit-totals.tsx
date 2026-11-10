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
import { useTranslation } from 'react-i18next'

import { Skeleton } from '@/components/ui/skeleton'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatPercent, formatQuota } from '@/lib/format'
import { cn } from '@/lib/utils'

import { getProfitMargin } from '../lib/report'
import type { ProfitTotals as ProfitTotalsData } from '../types'

type ProfitTotalsProps = {
  title?: string
  totals?: ProfitTotalsData
  loading?: boolean
  className?: string
}

/** Revenue, upstream cost and profit of one period, with its volume. */
export function ProfitTotals(props: ProfitTotalsProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const totals = props.totals
  const margin = totals ? getProfitMargin(totals) : null

  const amounts = [
    { key: 'revenue', label: t('Revenue'), value: totals?.revenue },
    { key: 'cost', label: t('Upstream cost'), value: totals?.cost },
    { key: 'profit', label: t('Profit'), value: totals?.profit },
  ]

  return (
    <div
      className={cn(
        'bg-background/60 flex flex-col gap-2 rounded-xl border p-3',
        props.className
      )}
    >
      {props.title ? (
        <div className='text-muted-foreground text-xs font-medium'>
          {props.title}
        </div>
      ) : null}
      <dl className='grid grid-cols-3 gap-2'>
        {amounts.map((amount) => (
          <div key={amount.key} className='flex min-w-0 flex-col gap-1'>
            <dt className='text-muted-foreground truncate text-[11px]'>
              {amount.label}
            </dt>
            <dd
              className={cn(
                'truncate font-mono text-sm font-semibold tabular-nums',
                amount.key === 'profit' &&
                  (amount.value ?? 0) < 0 &&
                  'text-destructive'
              )}
            >
              {props.loading || amount.value === undefined ? (
                <Skeleton className='h-5 w-16' />
              ) : (
                formatQuota(amount.value)
              )}
            </dd>
          </div>
        ))}
      </dl>
      {totals && !props.loading ? (
        <div className='text-muted-foreground flex flex-wrap gap-x-3 gap-y-1 text-[11px]'>
          <span>
            {t('Requests')}: {formatNumber(totals.request_count, locale)}
          </span>
          <span>
            {t('Images')}: {formatNumber(totals.image_count, locale)}
          </span>
          <span>
            {t('Margin')}: {margin === null ? '-' : formatPercent(margin * 100)}
          </span>
          {totals.uncosted_count > 0 ? (
            <span className='text-warning'>
              {t('{{count}} requests have no upstream cost set', {
                count: totals.uncosted_count,
              })}
            </span>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}
