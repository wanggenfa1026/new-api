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
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { ArrowRight } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { requireServerSuccess } from '@/lib/server-error-message'

import { getProfitSummary } from '../api'
import { ProfitTotals } from './profit-totals'

const PERIODS = [
  { key: 'today', labelKey: 'Today' },
  { key: 'yesterday', labelKey: 'Yesterday' },
  { key: 'month', labelKey: 'Month to date' },
] as const

/** Profit of today, yesterday and this month on the administrator overview. */
export function ProfitSummaryPanel() {
  const { t } = useTranslation()
  const summaryQuery = useQuery({
    queryKey: ['profit', 'summary'],
    queryFn: async () => requireServerSuccess(await getProfitSummary()),
    staleTime: 60 * 1000,
  })
  const summary = summaryQuery.data?.data

  return (
    <section className='bg-card flex flex-col gap-3 rounded-2xl border p-3 shadow-xs sm:p-5'>
      <div className='flex flex-wrap items-start justify-between gap-3'>
        <div className='flex flex-col gap-1'>
          <h3 className='text-sm font-semibold sm:text-base'>{t('Profit')}</h3>
          <p className='text-muted-foreground text-xs sm:text-sm'>
            {t('What keys paid minus what the upstream cost')}
          </p>
        </div>
        <Button
          variant='ghost'
          size='sm'
          render={<Link to='/profit' />}
          nativeButton={false}
        >
          {t('View report')}
          <ArrowRight aria-hidden='true' />
        </Button>
      </div>
      <div className='grid gap-3 md:grid-cols-3'>
        {PERIODS.map((period) => (
          <ProfitTotals
            key={period.key}
            title={t(period.labelKey)}
            totals={summary?.[period.key]}
            loading={summaryQuery.isLoading}
          />
        ))}
      </div>
    </section>
  )
}
