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

import type {
  ApiResponse,
  ChannelCost,
  ChannelCostUpdate,
  ProfitReport,
  ProfitReportParams,
  ProfitSummary,
} from './types'

/** Profit of today, yesterday and the current month. */
export async function getProfitSummary(): Promise<ApiResponse<ProfitSummary>> {
  const res = await api.get('/api/profit/summary')
  return res.data
}

/** Profit of a day range grouped by day, key, channel or model. */
export async function getProfitReport(
  params: ProfitReportParams
): Promise<ApiResponse<ProfitReport>> {
  const query = new URLSearchParams({
    start_day: String(params.startDay),
    end_day: String(params.endDay),
    group_by: params.groupBy,
    include_excluded: String(params.includeExcluded),
  })
  const res = await api.get(`/api/profit/report?${query.toString()}`)
  return res.data
}

/** Upstream cost of every channel and model. */
export async function getChannelCosts(): Promise<ApiResponse<ChannelCost[]>> {
  const res = await api.get('/api/profit/costs')
  return res.data
}

/** Set or remove upstream costs; a null cost removes it. */
export async function updateChannelCosts(
  items: ChannelCostUpdate[]
): Promise<ApiResponse> {
  const res = await api.put('/api/profit/costs', { items })
  return res.data
}

/** Leave a key out of, or count it in, profit statistics. */
export async function setTokenProfitExcluded(
  tokenId: number,
  profitExcluded: boolean
): Promise<ApiResponse> {
  const res = await api.put(`/api/profit/token/${tokenId}`, {
    profit_excluded: profitExcluded,
  })
  return res.data
}
