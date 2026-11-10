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
import { quotaUnitsToDollars } from '@/lib/format'

import type { ProfitGroupBy, ProfitReportRow, ProfitTotals } from '../types'

type Translate = (key: string, options?: Record<string, unknown>) => string

/** A day as the YYYYMMDD number the profit API uses, from a YYYY-MM-DD input value. */
export function dayFromInputValue(value: string): number {
  return Number(value.replaceAll('-', ''))
}

/** A YYYYMMDD day as YYYY-MM-DD, for date inputs and for display. */
export function formatStatDay(day: number): string {
  const text = String(day).padStart(8, '0')
  return `${text.slice(0, 4)}-${text.slice(4, 6)}-${text.slice(6, 8)}`
}

/** What a report row is about: its day, key, channel or model. */
export function getProfitRowLabel(
  row: ProfitReportRow,
  groupBy: ProfitGroupBy,
  t: Translate
): string {
  if (groupBy === 'day') return formatStatDay(row.stat_day ?? 0)
  if (groupBy === 'model') return row.model_name ?? ''
  if (groupBy === 'channel') {
    return row.channel_name || t('Channel #{{id}}', { id: row.channel_id ?? 0 })
  }
  if (!row.token_id) return t('No API key')
  return row.token_name || t('Key #{{id}}', { id: row.token_id })
}

/** Share of the revenue kept as profit, or null when nothing was charged. */
export function getProfitMargin(totals: ProfitTotals): number | null {
  if (totals.revenue <= 0) return null
  return totals.profit / totals.revenue
}

const GROUP_COLUMN_KEYS: Record<ProfitGroupBy, string> = {
  day: 'Date',
  token: 'API Key',
  channel: 'Channel',
  model: 'Model',
}

export function getProfitGroupColumnTitle(
  groupBy: ProfitGroupBy,
  t: Translate
): string {
  return t(GROUP_COLUMN_KEYS[groupBy])
}

/** Column titles of the exported profit report. */
export function getProfitCsvHeader(
  groupBy: ProfitGroupBy,
  t: Translate
): string[] {
  return [
    getProfitGroupColumnTitle(groupBy, t),
    ...(groupBy === 'token' ? [t('Counted in profit')] : []),
    t('Requests'),
    t('Images'),
    t('Revenue'),
    t('Upstream cost'),
    t('Profit'),
    t('Requests without a cost'),
  ]
}

/** One report row as the cells under getProfitCsvHeader; amounts are plain numbers. */
export function getProfitCsvRow(
  row: ProfitReportRow,
  groupBy: ProfitGroupBy,
  t: Translate
): string[] {
  const amount = (units: number) => quotaUnitsToDollars(units).toFixed(6)
  return [
    getProfitRowLabel(row, groupBy, t),
    ...(groupBy === 'token' ? [row.profit_excluded ? t('No') : t('Yes')] : []),
    String(row.request_count),
    String(row.image_count),
    amount(row.revenue),
    amount(row.cost),
    amount(row.profit),
    String(row.uncosted_count),
  ]
}
