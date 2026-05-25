import client from './client'

function extractData<T>(res: { data: unknown }, fallback: T): T {
	const d = res.data as Record<string, unknown>
	if (!d) return fallback
	if (d.data !== undefined && d.data !== null) return d.data as T
	if (typeof d === 'object' && !Array.isArray(d) && !('code' in d)) return d as T
	return fallback
}

function extractAmountData(res: { data: unknown }): number {
	const d = res.data as Record<string, unknown>
	if (!d) return 0
	if (typeof d.data === 'number') return d.data
	return 0
}

function normalizeTransaction(tx: any): Transaction {
  let createdAt = tx.created_at || tx.createdAt || ''
  if (createdAt && typeof createdAt === 'object') {
    const seconds = createdAt.seconds || createdAt.Seconds || 0
    const nanos = createdAt.nanos || createdAt.Nanos || 0
    const ms = (typeof seconds === 'string' ? parseInt(seconds) : seconds) * 1000 + Math.floor((typeof nanos === 'number' ? nanos : 0) / 1000000)
    if (ms > 0) createdAt = new Date(ms).toISOString()
    else createdAt = ''
  }
  return {
    id: tx.id || '',
    user_id: tx.user_id || tx.userId || '',
    account_id: tx.account_id || tx.accountId || '',
    type: typeof tx.type === 'number' ? tx.type : (typeof tx.type === 'string' ? parseInt(tx.type) || 0 : 0),
    item: typeof tx.item === 'number' ? tx.item : (typeof tx.item === 'string' ? parseInt(tx.item) || 0 : 0),
    amount: typeof tx.amount === 'number' ? tx.amount : parseFloat(tx.amount) || 0,
    balance_before: typeof (tx.balance_before ?? tx.balanceBefore) === 'number' ? (tx.balance_before ?? tx.balanceBefore) : parseFloat(tx.balance_before ?? tx.balanceBefore) || 0,
    balance_after: typeof (tx.balance_after ?? tx.balanceAfter) === 'number' ? (tx.balance_after ?? tx.balanceAfter) : parseFloat(tx.balance_after ?? tx.balanceAfter) || 0,
    description: tx.description || '',
    status: typeof tx.status === 'number' ? tx.status : (typeof tx.status === 'string' ? parseInt(tx.status) || 0 : 0),
    metadata: tx.metadata || {},
    created_at: createdAt,
  }
}

function extractTransactionsData(res: { data: unknown }): { transactions: Transaction[]; total: number } {
	const d = res.data as Record<string, unknown>
	if (!d) return { transactions: [], total: 0 }
	if (d.data) {
		const data = d.data as Record<string, unknown>
		if (Array.isArray(data.transactions)) {
			return {
				transactions: data.transactions.map(normalizeTransaction),
				total: typeof data.total === 'number' ? data.total : data.transactions.length
			}
		}
	}
	return { transactions: [], total: 0 }
}

export interface Account {
	id: string
	user_id: string
	balance: number
	credit_limit: number
	usage: Record<string, number>
	created_at: string
	updated_at: string
}

export interface Transaction {
	id: string
	user_id: string
	account_id: string
	type: number
	item: number
	amount: number
	balance_before: number
	balance_after: number
	description: string
	status: number
	metadata: Record<string, string>
	created_at: string
}

export interface DepositRequest {
	amount: number
	payment_method?: string
	metadata?: Record<string, string>
}

export interface WithdrawRequest {
	amount: number
	withdraw_method?: string
	metadata?: Record<string, string>
}

export interface RefundRequest {
	transaction_id: string
	amount: number
	reason: string
	metadata?: Record<string, string>
}

export interface ConsumeModelCallRequest {
	provider: string
	model_name: string
	token_count: number
	metadata?: Record<string, string>
}

export interface ConsumeEmbeddingRequest {
	provider: string
	model_name: string
	token_count: number
	vector_count: number
	metadata?: Record<string, string>
}

export interface ConsumeStorageRequest {
	storage_type: string
	size_bytes: number
	metadata?: Record<string, string>
}

export interface ConsumeBandwidthRequest {
	bandwidth_type: string
	bytes: number
	metadata?: Record<string, string>
}

export interface GetTransactionsParams {
	type?: number
	start_time?: string
	end_time?: string
	page?: number
	page_size?: number
}

export interface GetUsageStatsParams {
	item?: number
	start_time?: string
	end_time?: string
}

export const billingApi = {
	async deposit(data: DepositRequest) {
		const res = await client.post('/billing/deposit', data)
		return extractData<Transaction>(res, null)
	},

	async withdraw(data: WithdrawRequest) {
		const res = await client.post('/billing/withdraw', data)
		return extractData<Transaction>(res, null)
	},

	async refund(data: RefundRequest) {
		const res = await client.post('/billing/refund', data)
		return extractData<Transaction>(res, null)
	},

	async getAccount() {
		const res = await client.get('/billing/account')
		return extractData<Account>(res, null)
	},

	async getTransactions(params?: GetTransactionsParams) {
		const res = await client.get('/billing/transactions', { params })
		return extractTransactionsData(res)
	},

	async getUsageStats(params?: GetUsageStatsParams) {
		const res = await client.get('/billing/usage-stats', { params })
		return extractData<{ stats: Record<string, number> }>(res, { stats: {} })
	},

	async consumeModelCall(data: ConsumeModelCallRequest) {
		const res = await client.post('/billing/consume/model-call', data)
		return extractAmountData(res)
	},

	async consumeEmbedding(data: ConsumeEmbeddingRequest) {
		const res = await client.post('/billing/consume/embedding', data)
		return extractAmountData(res)
	},

	async consumeStorage(data: ConsumeStorageRequest) {
		const res = await client.post('/billing/consume/storage', data)
		return extractAmountData(res)
	},

	async consumeBandwidth(data: ConsumeBandwidthRequest) {
		const res = await client.post('/billing/consume/bandwidth', data)
		return extractAmountData(res)
	}
}
