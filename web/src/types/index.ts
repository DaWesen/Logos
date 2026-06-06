export interface User {
  id: string
  username: string
  nickname: string
  avatar: string
  email: string
  status: 'online' | 'offline' | 'busy' | 'away'
}

export interface Message {
  id: string
  chatId: string
  senderId: string
  senderName: string
  senderAvatar: string
  content: string
  messageType: 'text' | 'image' | 'file' | 'voice' | 'video' | 'location' | 'system' | 'withdrawn'
  mediaUrl?: string
  mediaMeta?: string
  replyTo?: string
  createdAt: string
  editedAt?: string
  isBot?: boolean
  translatedContent?: string
  isRead?: boolean
  uploading?: boolean
  uploadProgress?: number
}

export interface Chat {
  id: string
  type: 'private' | 'group'
  name: string
  avatar: string
  lastMessage?: string
  lastMessageTime?: string
  unreadCount: number
  members?: User[]
  isBot?: boolean
}

export interface Group {
  id: string
  name: string
  avatar: string
  description: string
  owner: string
  members: GroupMember[]
  createdAt: string
}

export interface GroupMember {
  userId: string
  nickname: string
  avatar: string
  role: 'owner' | 'admin' | 'member'
  joinedAt: string
}

export interface Bot {
  id: string
  name: string
  avatar: string
  description: string
  systemPrompt: string
  model: string
  temperature: number
  enableMemory: boolean
  enableRag: boolean
  knowledgeBaseIds: string[]
  createdAt: string
}

export interface KnowledgeBase {
  id: string
  name: string
  description: string
  documentCount: number
  status: 'ready' | 'processing' | 'error'
  createdAt: string
}

export interface FriendRequest {
  id: string
  fromUserId: string
  fromNickname: string
  fromAvatar: string
  status: 'pending' | 'accepted' | 'rejected'
  message: string
  createdAt: string
}

export interface Friend {
  userId: string
  nickname: string
  avatar: string
  status: 'online' | 'offline' | 'busy' | 'away'
  addedAt: string
}

export interface ApiResponse<T = unknown> {
  code: number
  message: string
  data: T
}

export interface PaginatedData<T> {
  items: T[]
  total: number
  page: number
  pageSize: number
}

export interface LoginRequest {
  username: string
  password: string
}

export interface RegisterRequest {
  username: string
  password: string
  nickname: string
  email: string
}

export interface LoginResponse {
  token: string
  user: User
}

export interface WsMessage {
  type: 'connect' | 'message' | 'typing' | 'read_receipt' | 'online_status' | 'withdraw' | 'notification' | 'presence' | 'error' | 'heartbeat' | 'ack'
  data: unknown
  request_id?: string
  payload?: unknown
}
