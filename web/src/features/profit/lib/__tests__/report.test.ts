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
import { describe, expect, test } from 'vitest'

import { quotaUnitsToDollars } from '@/lib/format'

import type { ProfitReportRow } from '../../types'
import {
  dayFromInputValue,
  formatStatDay,
  getProfitCsvHeader,
  getProfitCsvRow,
  getProfitMargin,
  getProfitRowLabel,
} from '../report'

const t = (key: string, options?: Record<string, unknown>) =>
  key.replaceAll(/{{(\w+)}}/g, (_, name: string) => String(options?.[name]))

const totals = {
  request_count: 3,
  image_count: 4,
  revenue: 16500,
  cost: 9000,
  profit: 7500,
  uncosted_count: 1,
}

describe('profit report days', () => {
  test('a date input value becomes the day number the API uses and back', () => {
    expect(dayFromInputValue('2026-11-09')).toBe(20261109)
    expect(formatStatDay(20261109)).toBe('2026-11-09')
  })
})

describe('profit row label', () => {
  test('each grouping names its own row', () => {
    expect(getProfitRowLabel({ ...totals, stat_day: 20261109 }, 'day', t)).toBe(
      '2026-11-09'
    )
    expect(
      getProfitRowLabel({ ...totals, model_name: 'gpt-image-2' }, 'model', t)
    ).toBe('gpt-image-2')
    expect(
      getProfitRowLabel(
        { ...totals, channel_id: 2, channel_name: 'main' },
        'channel',
        t
      )
    ).toBe('main')
    expect(
      getProfitRowLabel(
        { ...totals, token_id: 11, token_name: 'customer' },
        'token',
        t
      )
    ).toBe('customer')
  })

  test('a deleted channel, an unnamed key and a request without a key still get a label', () => {
    expect(getProfitRowLabel({ ...totals, channel_id: 9 }, 'channel', t)).toBe(
      'Channel #9'
    )
    expect(getProfitRowLabel({ ...totals, token_id: 11 }, 'token', t)).toBe(
      'Key #11'
    )
    expect(getProfitRowLabel({ ...totals }, 'token', t)).toBe('No API key')
  })
})

describe('profit margin', () => {
  test('is the share of revenue kept as profit', () => {
    expect(getProfitMargin(totals)).toBeCloseTo(7500 / 16500)
  })

  test('is unknown when nothing was charged', () => {
    expect(getProfitMargin({ ...totals, revenue: 0, profit: -9000 })).toBeNull()
  })
})

describe('profit CSV', () => {
  test('the key grouping adds whether the key is counted, and rows match the header', () => {
    const row: ProfitReportRow = {
      ...totals,
      token_id: 11,
      token_name: 'customer',
      profit_excluded: true,
    }

    const header = getProfitCsvHeader('token', t)
    const cells = getProfitCsvRow(row, 'token', t)

    expect(header).toEqual([
      'API Key',
      'Counted in profit',
      'Requests',
      'Images',
      'Revenue',
      'Upstream cost',
      'Profit',
      'Requests without a cost',
    ])
    expect(cells).toHaveLength(header.length)
    expect(cells.slice(0, 4)).toEqual(['customer', 'No', '3', '4'])
    expect(cells[7]).toBe('1')
  })

  test('amounts are plain numbers in the display currency, not formatted text', () => {
    const cells = getProfitCsvRow({ ...totals, stat_day: 20261109 }, 'day', t)

    expect(getProfitCsvHeader('day', t)[0]).toBe('Date')
    expect(cells).toEqual([
      '2026-11-09',
      '3',
      '4',
      quotaUnitsToDollars(16500).toFixed(6),
      quotaUnitsToDollars(9000).toFixed(6),
      quotaUnitsToDollars(7500).toFixed(6),
      '1',
    ])
    expect(cells[3]).toMatch(/^\d+\.\d{6}$/)
  })
})
