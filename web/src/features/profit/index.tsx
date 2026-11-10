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
import { Download } from 'lucide-react'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { downloadCsv, toCsvLine } from '@/features/usage-logs/lib/export'
import dayjs from '@/lib/dayjs'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import { getProfitReport, setTokenProfitExcluded } from './api'
import { ChannelCostsEditor } from './components/channel-costs-editor'
import { ProfitReportTable } from './components/profit-report-table'
import { ProfitTotals } from './components/profit-totals'
import {
  dayFromInputValue,
  getProfitCsvHeader,
  getProfitCsvRow,
} from './lib/report'
import type { ProfitGroupBy } from './types'

const DAY_INPUT_FORMAT = 'YYYY-MM-DD'
const DEFAULT_RANGE_DAYS = 30
const COSTS_TAB = 'costs'

type ProfitTab = ProfitGroupBy | typeof COSTS_TAB

const REPORT_TABS: { value: ProfitGroupBy; labelKey: string }[] = [
  { value: 'day', labelKey: 'By day' },
  { value: 'token', labelKey: 'By API key' },
  { value: 'channel', labelKey: 'By channel' },
  { value: 'model', labelKey: 'By model' },
]

export function Profit() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const startInputId = useId()
  const endInputId = useId()
  const excludedSwitchId = useId()

  const [tab, setTab] = useState<ProfitTab>('day')
  const [startDay, setStartDay] = useState(() =>
    dayjs()
      .subtract(DEFAULT_RANGE_DAYS - 1, 'day')
      .format(DAY_INPUT_FORMAT)
  )
  const [endDay, setEndDay] = useState(() => dayjs().format(DAY_INPUT_FORMAT))
  const [includeExcluded, setIncludeExcluded] = useState(false)

  const groupBy: ProfitGroupBy = tab === COSTS_TAB ? 'day' : tab
  const rangeValid = startDay !== '' && endDay !== '' && startDay <= endDay

  const reportQuery = useQuery({
    queryKey: ['profit', 'report', groupBy, startDay, endDay, includeExcluded],
    queryFn: async () =>
      requireServerSuccess(
        await getProfitReport({
          startDay: dayFromInputValue(startDay),
          endDay: dayFromInputValue(endDay),
          groupBy,
          includeExcluded,
        })
      ),
    enabled: tab !== COSTS_TAB && rangeValid,
  })
  const report = reportQuery.data?.data
  const rows = report?.rows ?? []

  const countedMutation = useMutation({
    mutationFn: async (input: { tokenId: number; counted: boolean }) =>
      requireServerSuccess(
        await setTokenProfitExcluded(input.tokenId, !input.counted)
      ),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['profit'] }),
    onError: (error) => handleServerError(error, t('Failed to update the key')),
  })

  const exportReport = () => {
    downloadCsv(
      `profit-${groupBy}-${dayFromInputValue(startDay)}-${dayFromInputValue(endDay)}.csv`,
      [
        toCsvLine(getProfitCsvHeader(groupBy, t)),
        ...rows.map((row) => toCsvLine(getProfitCsvRow(row, groupBy, t))),
      ]
    )
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Profit')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button
          variant='outline'
          onClick={exportReport}
          disabled={tab === COSTS_TAB || rows.length === 0}
        >
          <Download aria-hidden='true' />
          {t('Export CSV')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <Tabs
          value={tab}
          onValueChange={(value) => setTab(value as ProfitTab)}
          className='gap-4'
        >
          <TabsList>
            {REPORT_TABS.map((item) => (
              <TabsTrigger key={item.value} value={item.value}>
                {t(item.labelKey)}
              </TabsTrigger>
            ))}
            <TabsTrigger value={COSTS_TAB}>{t('Upstream costs')}</TabsTrigger>
          </TabsList>

          {REPORT_TABS.map((item) => (
            <TabsContent
              key={item.value}
              value={item.value}
              className='flex flex-col gap-4'
            >
              <div className='flex flex-wrap items-end gap-4'>
                <div className='flex flex-col gap-1.5'>
                  <Label htmlFor={startInputId}>{t('Start date')}</Label>
                  <Input
                    id={startInputId}
                    type='date'
                    className='w-40'
                    value={startDay}
                    max={endDay}
                    onChange={(event) => setStartDay(event.target.value)}
                  />
                </div>
                <div className='flex flex-col gap-1.5'>
                  <Label htmlFor={endInputId}>{t('End date')}</Label>
                  <Input
                    id={endInputId}
                    type='date'
                    className='w-40'
                    value={endDay}
                    min={startDay}
                    onChange={(event) => setEndDay(event.target.value)}
                  />
                </div>
                <div className='flex h-9 items-center gap-2'>
                  <Switch
                    id={excludedSwitchId}
                    checked={includeExcluded}
                    onCheckedChange={setIncludeExcluded}
                  />
                  <Label htmlFor={excludedSwitchId}>
                    {t('Include keys left out of profit')}
                  </Label>
                </div>
              </div>
              <ProfitTotals
                totals={report?.totals}
                loading={reportQuery.isLoading}
              />
              <ProfitReportTable
                rows={rows}
                groupBy={item.value}
                pendingTokenId={
                  countedMutation.isPending
                    ? countedMutation.variables?.tokenId
                    : undefined
                }
                onCountedChange={(tokenId, counted) =>
                  countedMutation.mutate({ tokenId, counted })
                }
              />
            </TabsContent>
          ))}

          <TabsContent value={COSTS_TAB}>
            <ChannelCostsEditor />
          </TabsContent>
        </Tabs>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
