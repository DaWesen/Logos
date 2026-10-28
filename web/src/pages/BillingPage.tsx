import { useState, useEffect } from 'react'
import { billingApi, Account, Transaction, PaymentOrder, PaymentOrderStatus } from '../api/billing'
import './BillingPage.css'

export function BillingPage() {
  const [account, setAccount] = useState<Account | null>(null)
  const [transactions, setTransactions] = useState<Transaction[]>([])
  const [loading, setLoading] = useState(true)
  const [depositAmount, setDepositAmount] = useState('')
  const [depositing, setDepositing] = useState(false)
  const [paymentOrder, setPaymentOrder] = useState<PaymentOrder | null>(null)
  const [refreshingOrder, setRefreshingOrder] = useState(false)

  const loadData = async () => {
    try {
      setLoading(true)
      const [accountData, transactionsData] = await Promise.all([
        billingApi.getAccount(),
        billingApi.getTransactions({ page_size: 20 })
      ])
      setAccount(accountData)
      setTransactions(transactionsData.transactions || [])
    } catch (error) {
      console.error('Failed to load billing data:', error)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    loadData()
  }, [])

  const handleDeposit = async (e: React.FormEvent) => {
    e.preventDefault()
    const amount = parseFloat(depositAmount)
    if (!amount || amount <= 0) return

    try {
      setDepositing(true)
      // 只创建支付单：金额由支付渠道回调入账，前端无法直接给账户加钱
      const order = await billingApi.createPaymentOrder({
        amount,
        payment_method: 'alipay'
      })
      if (!order) {
        alert('创建支付单失败，请重试')
        return
      }
      setPaymentOrder(order)
      setDepositAmount('')
    } catch (error) {
      console.error('Failed to create payment order:', error)
      alert(error instanceof Error ? error.message : '创建支付单失败，请重试')
    } finally {
      setDepositing(false)
    }
  }

  const refreshPaymentOrder = async () => {
    if (!paymentOrder) return
    try {
      setRefreshingOrder(true)
      const latest = await billingApi.getPaymentOrder(paymentOrder.id)
      if (latest) {
        setPaymentOrder(latest)
        if (latest.status === PaymentOrderStatus.PAID) await loadData()
      }
    } catch (error) {
      console.error('Failed to refresh payment order:', error)
    } finally {
      setRefreshingOrder(false)
    }
  }

  // 待支付时轮询订单状态，渠道回调到账后自动刷新余额
  useEffect(() => {
    if (!paymentOrder || paymentOrder.status !== PaymentOrderStatus.PENDING) return
    const orderId = paymentOrder.id
    const timer = setInterval(async () => {
      try {
        const latest = await billingApi.getPaymentOrder(orderId)
        if (!latest) return
        setPaymentOrder(latest)
        if (latest.status === PaymentOrderStatus.PAID) await loadData()
      } catch (error) {
        console.error('Failed to poll payment order:', error)
      }
    }, 5000)
    return () => clearInterval(timer)
  }, [paymentOrder?.id, paymentOrder?.status])

  const getPaymentStatusLabel = (status: number) => {
    const labels: Record<number, string> = {
      [PaymentOrderStatus.PENDING]: '待支付',
      [PaymentOrderStatus.PAID]: '已支付',
      [PaymentOrderStatus.FAILED]: '支付失败',
      [PaymentOrderStatus.EXPIRED]: '已过期'
    }
    return labels[status] || '未知'
  }

  const getTransactionTypeLabel = (type: number) => {
    const labels: Record<number, string> = {
      0: '未知',
      1: '充值',
      2: '提现',
      3: '消费',
      4: '退款'
    }
    return labels[type] || '未知'
  }

  const getTransactionTypeColor = (type: number) => {
    const colors: Record<number, string> = {
      1: 'green',
      2: 'red',
      3: 'orange',
      4: 'blue'
    }
    return colors[type] || 'gray'
  }

  if (loading) {
    return <div className="billing-loading">加载中...</div>
  }

  return (
    <div className="billing-page">
      <h1>钱包中心</h1>

      <div className="billing-card">
        <h2>账户余额</h2>
        <div className="balance-display">
          <span className="balance-amount">¥{account?.balance != null ? Number(account.balance).toFixed(6) : '0.000000'}</span>
        </div>

        <form onSubmit={handleDeposit} className="deposit-form">
          <input
            type="number"
            step="0.01"
            min="0.01"
            value={depositAmount}
            onChange={(e) => setDepositAmount(e.target.value)}
            placeholder="输入充值金额"
            disabled={depositing}
          />
          <button type="submit" disabled={depositing || !depositAmount}>
            {depositing ? '生成中...' : '充值'}
          </button>
        </form>

        {paymentOrder && (
          <div className="payment-order">
            <div className="payment-order-header">
              <span className="payment-order-id">支付单号 {paymentOrder.id}</span>
              <span className={`payment-order-status payment-order-status-${paymentOrder.status}`}>
                {getPaymentStatusLabel(paymentOrder.status)}
              </span>
            </div>
            <div className="payment-order-meta">
              金额 ¥{paymentOrder.amount.toFixed(2)} · 支付方式 {paymentOrder.payment_method}
              {paymentOrder.status === PaymentOrderStatus.PENDING && paymentOrder.expires_at && (
                <> · 有效期至 {new Date(paymentOrder.expires_at).toLocaleTimeString()}</>
              )}
            </div>
            <div className="payment-order-tip">
              请通过 {paymentOrder.payment_method} 完成支付，渠道回调成功后余额自动到账（本页会自动刷新状态）。
            </div>
            <div className="payment-order-actions">
              <button type="button" onClick={refreshPaymentOrder} disabled={refreshingOrder}>
                {refreshingOrder ? '刷新中...' : '刷新支付状态'}
              </button>
              <button type="button" className="payment-order-close" onClick={() => setPaymentOrder(null)}>
                关闭
              </button>
            </div>
          </div>
        )}
      </div>

      <div className="billing-card">
        <h2>使用说明</h2>
        <div className="usage-info">
          <p>📌 <strong>计费方式：</strong>按百万tokens计费，区分输入/输出</p>
          <p>💡 <strong>定价标准：</strong></p>
          <table className="pricing-table">
            <thead>
              <tr><th></th><th>标准模型</th><th>高级模型</th></tr>
            </thead>
            <tbody>
              <tr><td>输入（缓存未命中）</td><td>1元</td><td>3元</td></tr>
              <tr><td>输入（缓存命中）</td><td>0.02元</td><td>0.025元</td></tr>
              <tr><td>输出</td><td>2元</td><td>6元</td></tr>
            </tbody>
          </table>
          <p style={{fontSize: '12px', color: 'var(--ba-text-light)'}}>1个英文字符 ≈ 0.3 token，1个中文字符 ≈ 0.6 token</p>
          <p>💡 <strong>使用步骤：</strong></p>
          <ol>
            <li>在此页面充值你的账户</li>
            <li>创建 Bot 并进行对话，系统会自动扣除相应费用</li>
            <li>无论使用平台模型还是自定义API，均会本地计费</li>
            <li>在下方查看你的交易记录和消费详情</li>
          </ol>
        </div>
      </div>

      <div className="billing-card">
        <h2>交易记录</h2>
        <div className="transactions-list">
          {!transactions || transactions.length === 0 ? (
            <div className="no-transactions">暂无交易记录</div>
          ) : (
            transactions.map((tx) => (
              <div key={tx.id} className="transaction-item">
                <div className="transaction-left">
                  <span
                    className={`transaction-type transaction-type-${getTransactionTypeColor(tx.type)}`}
                  >
                    {getTransactionTypeLabel(tx.type)}
                  </span>
                  <span className="transaction-description">{tx.description}</span>
                  {tx.metadata && (tx.metadata.input_tokens || tx.metadata.output_tokens) && (
                    <span className="transaction-tokens">
                      输入 {parseInt(tx.metadata.input_tokens || '0').toLocaleString()} / 输出 {parseInt(tx.metadata.output_tokens || '0').toLocaleString()} tokens
                      {tx.metadata.cache_hit_tokens && parseInt(tx.metadata.cache_hit_tokens) > 0 && (
                        <> (缓存命中 {parseInt(tx.metadata.cache_hit_tokens).toLocaleString()})</>
                      )}
                    </span>
                  )}
                  {tx.metadata && !tx.metadata.input_tokens && tx.metadata.tokens && (
                    <span className="transaction-tokens">{parseInt(tx.metadata.tokens).toLocaleString()} tokens</span>
                  )}
                  {tx.metadata && tx.metadata.model && (
                    <span className="transaction-model">{tx.metadata.model}</span>
                  )}
                  {tx.metadata && tx.metadata.input_price && tx.metadata.output_price && (
                    <span className="transaction-price">¥{parseFloat(tx.metadata.input_price).toFixed(2)}/百万输入 · ¥{parseFloat(tx.metadata.output_price).toFixed(2)}/百万输出</span>
                  )}
                </div>
                <div className="transaction-right">
                  <span
                    className={`transaction-amount ${
                      tx.type === 1 || tx.type === 4 ? 'positive' : 'negative'
                    }`}
                  >
                    {tx.type === 1 || tx.type === 4 ? '+' : '-'}{Math.abs(tx.amount).toFixed(6)}
                  </span>
                  <span className="transaction-time">
                    {new Date(tx.created_at).toLocaleString()}
                  </span>
                </div>
              </div>
            ))
          )}
        </div>
      </div>
    </div>
  )
}
