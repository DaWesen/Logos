import axios from 'axios'

const client = axios.create({
  baseURL: '/api/v1',
  timeout: 30000,
  headers: { 'Content-Type': 'application/json' },
})

// 429重试计数
const retryCount = new Map<string, number>()

client.interceptors.request.use((config) => {
  const token = localStorage.getItem('aim_token')
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

let isRedirecting = false

client.interceptors.response.use(
  (res) => res,
  (err) => {
    if (err.response?.status === 401 && !isRedirecting) {
      isRedirecting = true
      localStorage.removeItem('aim_token')
      localStorage.removeItem('aim_user')
      setTimeout(() => {
        window.location.href = '/login'
        isRedirecting = false
      }, 100)
    }
    
    // 处理429错误，但限制重试次数
    if (err.response?.status === 429) {
      const requestKey = `${err.config.method}-${err.config.url}`
      const count = retryCount.get(requestKey) || 0
      
      // 最多重试2次
      if (count < 2) {
        retryCount.set(requestKey, count + 1)
        const retryAfter = parseInt(err.response.headers['retry-after'] || '2')
        return new Promise((resolve) => {
          setTimeout(() => {
            resolve(client(err.config))
          }, retryAfter * 1000)
        })
      } else {
        // 清除计数，不再重试
        retryCount.delete(requestKey)
      }
    }
    
    // 提取后端返回的错误消息
    let errorMsg = '操作失败'
    if (err.response?.data) {
      const data = err.response.data as Record<string, unknown>
      if (data.message) {
        errorMsg = String(data.message)
      } else if (data.msg) {
        errorMsg = String(data.msg)
      } else if (data.error) {
        errorMsg = String(data.error)
      }
    } else if (err.message) {
      errorMsg = err.message
    }
    
    // 创建一个新的 Error 对象，包含我们提取的消息
    const error = new Error(errorMsg)
    ;(error as any).original = err
    return Promise.reject(error)
  },
)

export default client
