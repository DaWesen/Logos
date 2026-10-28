import client from './client'

function extractData<T>(res: { data: unknown }, fallback: T | null): T | null {
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

// protobuf Timestamp 经 encoding/json 序列化为 {seconds, nanos}，统一转成 ISO 字符串
function normalizeProtoTime(value: unknown): string {
	if (!value) return ''
	if (typeof value === 'string') return value
	if (typeof value === 'object') {
		const v = value as Record<string, unknown>
		const seconds = Number(v.seconds ?? 0)
		const nanos = Number(v.nanos ?? 0)
		if (seconds > 0) return new Date(seconds * 1000 + Math.floor(nanos / 1000000)).toISOString()
	}
	return ''
}

function normalizePaymentOrder(raw: any): PaymentOrder {
	return {
		id: raw?.id || '',
		user_id: raw?.user_id || raw?.userId || '',
		amount: typeof raw?.amount === 'number' ? raw.amount : parseFloat(raw?.amount) || 0,
		payment_method: raw?.payment_method || raw?.paymentMethod || '',
		status: Number(raw?.status) || 0,
		trade_no: raw?.trade_no || raw?.tradeNo || '',
		metadata: raw?.metadata || {},
		created_at: normalizeProtoTime(raw?.created_at ?? raw?.createdAt),
		expires_at: normalizeProtoTime(raw?.expires_at ?? raw?.expiresAt),
		paid_at: normalizeProtoTime(raw?.paid_at ?? raw?.paidAt),
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

// 支付单状态，与服务端 PaymentOrderStatus 枚举保持一致
export const PaymentOrderStatus = {
	PENDING: 0,
	PAID: 1,
	FAILED: 2,
	EXPIRED: 3,
} as const

export interface PaymentOrder {
	id: string
	user_id: string
	amount: number
	payment_method: string
	status: number
	trade_no: string
	metadata: Record<string, string>
	created_at: string
	expires_at: string
	paid_at: string
}

export interface CreatePaymentOrderRequest {
	amount: number
	payment_method: string
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
	// 充值改为「创建支付单 -> 渠道回调入账」：前端只能创建支付单，不能直接给账户加钱
	async createPaymentOrder(data: CreatePaymentOrderRequest) {
		const res = await client.post('/billing/payments', data)
		const order = extractData<PaymentOrder>(res, null)
		return order ? normalizePaymentOrder(order) : null
	},

	async getPaymentOrder(id: string) {
		const res = await client.get(`/billing/payments/${id}`)
		const order = extractData<PaymentOrder>(res, null)
		return order ? normalizePaymentOrder(order) : null
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
