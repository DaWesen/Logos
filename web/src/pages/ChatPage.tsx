import { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import { Search, Plus, Bot, MoreVertical, Phone, Video, FileText, Sparkles, RefreshCw, Users, Crown, Shield, ShieldOff, UserMinus, LogOut, Smile, UserPlus, UserCheck, UserX, Camera, VolumeX, MessageSquare } from 'lucide-react'
import type { Message, WsMessage } from '@/types'
import { sendMessage, sendMediaMessage, uploadChatMedia, getChatHistory, markMessagesRead, toMediaUrl, editMessage, getConversationList, forwardMessage, deleteChat, deleteChatHistory, withdrawMessage, resolveMessageType, searchChatMessages } from '@/api/chat'
import { sendBotMessage, getBotList, getBotHistory } from '@/api/bot'
import { searchUsers, getUser } from '@/api/user'
import { getGroup, createGroup, getGroupMembers, inviteGroupMember, inviteBotToGroup, leaveGroup, kickGroupMember, joinGroup, updateGroupAvatar, muteGroupMember, transferGroupOwner, updateGroupAnnouncement, setGroupAdmin } from '@/api/group'
import { addFriend, handleFriendRequest, getFriendRequests, checkFriendship, deleteFriend } from '@/api/friend'
import { useWebSocket } from '@/hooks/useWebSocket'
import { useAuth } from '@/hooks/useAuth'
import { load, save } from '@/lib/store'
import client from '@/api/client'
import { getAISettings } from '@/api/settings'
import ChatBubble from '@/components/ChatBubble'
import MessageInput from '@/components/MessageInput'
import Modal from '@/components/Modal'
import './ChatPage.css'

// 直接连接到后端 Gateway，避免代理问题
const wsBase = `ws://localhost:8888/ws`

function privateChatId(myId: string, otherId: string): string {
  const a = String(myId)
  const b = String(otherId)
  return a < b ? `private_${a}_${b}` : `private_${b}_${a}`
}

interface ChatItem {
  id: string
  type: 'private' | 'bot' | 'group'
  name: string
  avatar: string
  lastMessage?: string
  lastMessageTime?: string
  unreadCount: number
  botId?: string
  userId?: string
  isFriend?: boolean
}

function detectMediaType(file: File): 'image' | 'video' | 'voice' | 'file' {
  const t = file.type || ''
  if (t.startsWith('image/')) return 'image'
  if (t.startsWith('video/')) return 'video'
  if (t.startsWith('audio/')) return 'voice'
  return 'file'
}

function resolveMediaMeta(raw: unknown): string {
  if (!raw) return ''
  if (typeof raw === 'string') {
    try {
      const parsed = JSON.parse(raw)
      if (parsed && typeof parsed === 'object') return parsed.filename || raw
    } catch { /* not JSON, return as-is */ }
    return raw
  }
  if (typeof raw === 'object') {
    const obj = raw as Record<string, unknown>
    return String(obj.filename || '')
  }
  return String(raw)
}

function loadAllMessages(): Record<string, Message[]> {
  try { return load<Record<string, Message[]>>('messages', {}) } catch { return {} }
}

function loadChatMessages(chatId: string): Message[] {
  return loadAllMessages()[chatId] || []
}

function persistMessages(chatId: string, msgs: Message[]) {
  try { const all = loadAllMessages(); all[chatId] = msgs; save('messages', all) } catch { /* */ }
}

export default function ChatPage() {
  const { user } = useAuth()
  const [chats, setChats] = useState<ChatItem[]>(() => load('chats', []))
  const [selectedChatId, setSelectedChatId] = useState<string | null>(null)
  const [messages, setMessages] = useState<Message[]>([])
  const [autoSaveToKb, setAutoSaveToKb] = useState(false)
  const [groupMembers, setGroupMembers] = useState<Map<string, { userId: string; username: string; avatar: string; role: string }>>(new Map())
  const chatsRef = useRef<ChatItem[]>(chats)
  const lastMarkReadTime = useRef<Record<string, number>>({})
  const [bots, setBots] = useState<{ id: string; name: string; avatar: string }[]>([])
  const [showBotPanel, setShowBotPanel] = useState(false)

  useEffect(() => {
    chatsRef.current = chats
  }, [chats])

  // 辅助函数：正确设置 senderName
  const getCorrectSenderName = useCallback((senderId: string, chatId: string): string => {
    if (user && user.id === senderId) {
      return user.nickname || user.username || '我'
    }
    if (senderId.startsWith('bot_')) {
      const botId = senderId.replace('bot_', '')
      const botInfo = bots.find(b => b.id === botId)
      if (botInfo) return botInfo.name
      const member = groupMembers.get(senderId)
      if (member) return member.username
      return 'Bot'
    }
    if (groupMembers.has(senderId)) {
      return groupMembers.get(senderId)!.username
    }
    const chat = chats.find((c) => c.id === chatId)
    if (chat && chat.type === 'private') {
      if (chat.userId === senderId) {
        return chat.name
      }
    }
    return '用户'
  }, [user, chats, groupMembers, bots])
  const [searchQuery, setSearchQuery] = useState('')
  const [showNewChat, setShowNewChat] = useState(false)
  const [showAddFriend, setShowAddFriend] = useState(false)
  const [showFriendRequests, setShowFriendRequests] = useState(false)
  const [addFriendId, setAddFriendId] = useState('')
  const [addFriendMsg, setAddFriendMsg] = useState('')
  const [addFriendError, setAddFriendError] = useState('')
  const [friendRequestList, setFriendRequestList] = useState<any[]>([])
  const [atBotMode, setAtBotMode] = useState(false)
  const [newChatName, setNewChatName] = useState('')
  const [newChatError, setNewChatError] = useState('')
  const [newChatLoading, setNewChatLoading] = useState(false)
  const messagesEndRef = useRef<HTMLDivElement>(null)
  const activeChatIdRef = useRef<string | null>(null)
  const avatarRef = useRef<HTMLInputElement>(null)
  const sentMessagesRef = useRef<Map<string, { localId: string; timestamp: number }>>(new Map())
  const mediaUrlToLocalIdRef = useRef<Map<string, string>>(new Map())
  const messageProcessingQueue = useRef<{ msg: WsMessage; resolve: () => void }[]>([])
  const isProcessingMessage = useRef(false)
  const [showMenu, setShowMenu] = useState(false)
  const [summary, setSummary] = useState('')
  const [summaryLoading, setSummaryLoading] = useState(false)
  const [showSummary, setShowSummary] = useState(false)
  const [summaryPanelType, setSummaryPanelType] = useState<'summary' | 'questions' | 'recommend'>('summary')
  const [summaryKeyPoints, setSummaryKeyPoints] = useState<string[]>([])
  const [summaryTodos, setSummaryTodos] = useState<any[]>([])
  const [summaryCandidates, setSummaryCandidates] = useState<any[]>([])
  const [summaryParticipants, setSummaryParticipants] = useState<string[]>([])
  const [showReplyCandidates, setShowReplyCandidates] = useState(false)
  const [replyCandidates, setReplyCandidates] = useState<any[]>([])
  const [replyLoading, setReplyLoading] = useState(false)
  const [summaryMessageCount, setSummaryMessageCount] = useState(50)
  const [pendingMentionIds, setPendingMentionIds] = useState<string[]>([])
  const menuRef = useRef<HTMLDivElement>(null)
  const [editingMessage, setEditingMessage] = useState<Message | null>(null)
  const [editContent, setEditContent] = useState('')
  const [showEditModal, setShowEditModal] = useState(false)
  const [showForwardModal, setShowForwardModal] = useState(false)
  const [forwardingMessage, setForwardingMessage] = useState<Message | null>(null)
  const [forwardChatId, setForwardChatId] = useState<string | null>(null)
  const [forwardChatType, setForwardChatType] = useState<'private' | 'group' | 'bot'>('private')
  const [showCreateGroup, setShowCreateGroup] = useState(false)
  const [newGroupName, setNewGroupName] = useState('')
  const [newGroupDescription, setNewGroupDescription] = useState('')
  const [createGroupLoading, setCreateGroupLoading] = useState(false)
  
  // 群组管理状态
  const [showJoinGroup, setShowJoinGroup] = useState(false)
  const [joinGroupId, setJoinGroupId] = useState('')
  const [joinGroupError, setJoinGroupError] = useState('')
  const [joinLoading, setJoinLoading] = useState(false)
  const [showAddMember, setShowAddMember] = useState(false)
  const [addMemberId, setAddMemberId] = useState('')
  const [addMemberError, setAddMemberError] = useState('')
  const [rightTab, setRightTab] = useState<'chat' | 'members' | 'announcement' | 'assistant'>('chat')
  const [showTransferOwner, setShowTransferOwner] = useState(false)
  const [transferToUserId, setTransferToUserId] = useState('')
  const [groupInfo, setGroupInfo] = useState<any>(null)
  const [collapsedGroups, setCollapsedGroups] = useState<Set<string>>(new Set())
  const [showMuteMember, setShowMuteMember] = useState(false)
  const [muteMemberId, setMuteMemberId] = useState('')
  const [muteDuration, setMuteDuration] = useState(0)
  const [showAnnouncement, setShowAnnouncement] = useState(false)
  const [announcementText, setAnnouncementText] = useState('')
  const [showSetAdmin, setShowSetAdmin] = useState(false)
  const [setAdminMemberId, setSetAdminMemberId] = useState('')
  const [setAdminIsAdmin, setSetAdminIsAdmin] = useState(false)
  const [showInviteBot, setShowInviteBot] = useState(false)
  const [inviteBotId, setInviteBotId] = useState('')
  const [showMentionBotList, setShowMentionBotList] = useState(false)
  const [mentionBotFilter, setMentionBotFilter] = useState('')
  const [typingChatId, setTypingChatId] = useState<string | null>(null)
  const typingTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const [onlineStatusMap, setOnlineStatusMap] = useState<Map<string, boolean>>(new Map())
  const [showSearchPanel, setShowSearchPanel] = useState(false)
  const [searchKeyword, setSearchKeyword] = useState('')
  const [searchStartTime, setSearchStartTime] = useState('')
  const [searchEndTime, setSearchEndTime] = useState('')
  const [searchResults, setSearchResults] = useState<Message[]>([])
  const [searchLoading, setSearchLoading] = useState(false)

  // 清理缓存函数
  const clearCache = useCallback(() => {
    if (confirm('确定要清理所有本地缓存吗？这将清除聊天记录和对话列表。')) {
      save('messages', {})
      save('chats', [])
      save('groups', [])
      setChats([])
      setMessages([])
      setSelectedChatId(null)
      alert('缓存已清理')
    }
  }, [])

  const selectedChat = chats.find((c) => c.id === selectedChatId) || null

  useEffect(() => { save('chats', chats) }, [chats])

  useEffect(() => {
    const handleClick = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) setShowMenu(false)
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [])

  const scrollToBottom = () => {
    setTimeout(() => {
      messagesEndRef.current?.scrollIntoView({ behavior: 'auto' })
    }, 50)
  }
  useEffect(() => { scrollToBottom() }, [messages])
  useEffect(() => {
    if (selectedChatId && messages.length > 0) {
      setTimeout(() => {
        messagesEndRef.current?.scrollIntoView({ behavior: 'auto' })
      }, 100)
    }
  }, [selectedChatId, messages.length])

  const persistAndSetMessages = useCallback((chatId: string, msgs: Message[]) => {
    // 只在 senderName 不存在或不正确时才修复
    const fixedMsgs = msgs.map((msg) => {
      // 如果消息已经有 senderName，优先使用消息里的，除非它是默认值
      const shouldFix = !msg.senderName || msg.senderName === '用户' || msg.senderName === ''
      return {
        ...msg,
        senderName: shouldFix ? getCorrectSenderName(msg.senderId, chatId) : msg.senderName
      }
    })
    persistMessages(chatId, fixedMsgs)
    if (activeChatIdRef.current === chatId) setMessages(fixedMsgs)
  }, [getCorrectSenderName, bots, user])

  const loadChatMessagesFromStore = useCallback((chatId: string) => {
    activeChatIdRef.current = chatId
    const rawMsgs = loadChatMessages(chatId)
    const isBotChatStore = chatId.startsWith('bot-') || chatId.startsWith('bot_')
    const fixedMsgs = rawMsgs.map((msg) => {
      const shouldFix = !msg.senderName || msg.senderName === '用户' || msg.senderName === ''
      let fixedName = shouldFix ? getCorrectSenderName(msg.senderId, chatId) : msg.senderName
      let fixedAvatar = msg.senderAvatar
      let fixedIsBot = msg.isBot

      // 如果是 Bot 聊天且不是自己发的，补充 Bot 信息
      if (isBotChatStore && msg.senderId !== user?.id) {
        const botId = msg.senderId || chatId.replace('bot-', '').replace('bot_', '')
        const botInfo = bots.find((b) => b.id === botId)
        if (botInfo) {
          fixedName = botInfo.name || fixedName || 'Bot'
          fixedAvatar = botInfo.avatar || fixedAvatar
          fixedIsBot = true
        }
      }

      return {
        ...msg,
        senderName: fixedName,
        senderAvatar: fixedAvatar,
        isBot: fixedIsBot,
        mediaUrl: msg.mediaUrl ? toMediaUrl(msg.mediaUrl) : undefined
      }
    })
    console.log('%c📂 [加载] 从localStorage加载消息', 'background: #607D8B; color: white; padding:2px 5px; border-radius:3px;', {
      chatId, msgCount: fixedMsgs.length,
      mediaMessages: fixedMsgs.filter(m => m.messageType !== 'text').map(m => ({
        id: m.id, type: m.messageType, mediaUrl: m.mediaUrl, mediaMeta: m.mediaMeta, content: `"${m.content}"`
      }))
    })
    const deduped = fixedMsgs.filter((msg, idx, arr) => {
      if (arr.findIndex(m => m.id === msg.id) !== idx) return false
      const contentKey = `${msg.senderId}:${(msg.content || '').trim()}`
      const earlier = arr.slice(0, idx).find(m =>
        m.senderId === msg.senderId &&
        (m.content || '').trim() === (msg.content || '').trim() &&
        Math.abs(new Date(m.createdAt).getTime() - new Date(msg.createdAt).getTime()) < 60000
      )
      if (earlier) return false
      return true
    })
    if (deduped.length !== fixedMsgs.length) {
      console.log(`%c🧹 [去重] 清理重复消息: ${fixedMsgs.length} -> ${deduped.length}`, 'background: #FF9800; color: white; padding:2px 5px; border-radius:3px;', { chatId })
      persistMessages(chatId, deduped)
    }
    if (activeChatIdRef.current === chatId) setMessages(deduped)
  }, [getCorrectSenderName, bots, user])

  const fetchRemoteMessages = useCallback(async (chatId: string) => {
    try {
      const isBotChat = chatId.startsWith('bot-') || chatId.startsWith('bot_')
      let data: any[]
      if (isBotChat) {
        const botId = chatId.replace(/^bot[-_]/, '')
        const botData = await getBotHistory(botId)
        data = (Array.isArray(botData) ? botData : []).map((m: Record<string, unknown>) => {
          const role = String(m.role || '')
          const isAssistant = role === 'assistant'
          let createdAt = m.created_at || m.createdAt || ''
          if (createdAt && typeof createdAt === 'object') {
            const seconds = (createdAt as any).seconds || (createdAt as any).Seconds || 0
            const nanos = (createdAt as any).nanos || (createdAt as any).Nanos || 0
            const ms = (typeof seconds === 'string' ? parseInt(seconds) : seconds) * 1000 + Math.floor((typeof nanos === 'number' ? nanos : 0) / 1000000)
            createdAt = ms > 0 ? new Date(ms).toISOString() : ''
          }
          return {
            ...m,
            chat_id: chatId,
            sender_id: isAssistant ? String(m.bot_id || botId) : (m.sender_id || user?.id || ''),
            is_bot: isAssistant,
            created_at: createdAt || new Date().toISOString(),
          }
        })
      } else {
        data = await getChatHistory(chatId)
      }
      const localMsgs = loadChatMessages(chatId)
      if (Array.isArray(data) && data.length > 0) {
        const remoteMsgs = data.map((m: Record<string, unknown>) => {
          const senderId = String(m.sender_id || m.senderId || '')
          const senderNameFromServer = String(m.sender_name || m.senderName || '')
          let finalSenderName = senderNameFromServer || getCorrectSenderName(senderId, chatId)
          
          // 从groupMembers中获取头像
          let senderAvatar = String(m.sender_avatar || m.senderAvatar || '')
          if (!senderAvatar && groupMembers.has(senderId)) {
            senderAvatar = groupMembers.get(senderId)!.avatar
          }
          if (!senderAvatar && chatsRef.current) {
            const chatItem = chatsRef.current.find(c => c.id === chatId)
            if (chatItem?.type === 'private' && chatItem.avatar) {
              senderAvatar = chatItem.avatar
            }
          }
          if (senderId === user?.id && !senderAvatar && user?.avatar) {
            senderAvatar = user.avatar
          }

          let isBotMsg = !!m.is_bot || !!m.isBot
          // 如果是 Bot 聊天且不是自己发的，补充 Bot 信息
          const isBotChatHistory = chatId.startsWith('bot-') || chatId.startsWith('bot_')
          if (isBotChatHistory && senderId !== user?.id) {
            const botId = senderId || chatId.replace('bot-', '').replace('bot_', '')
            const botInfo = bots.find((b) => b.id === botId)
            if (botInfo) {
              finalSenderName = botInfo.name || finalSenderName || 'Bot'
              senderAvatar = botInfo.avatar || senderAvatar
              isBotMsg = true
            }
          }
          
          const resolvedType = resolveMessageType(m.message_type || m.messageType)
          
          return {
            id: String(m.id || ''),
            chatId: String(m.chat_id || m.chatId || chatId),
            senderId: senderId,
            senderName: finalSenderName,
            senderAvatar: senderAvatar,
            content: String(m.content || '').trim(),
            messageType: resolvedType,
            mediaUrl: toMediaUrl(String(m.media_url || m.mediaUrl || '')),
            mediaMeta: resolveMediaMeta(m.media_meta || m.mediaMeta),
            createdAt: (() => {
              const raw = m.created_at || m.createdAt
              if (!raw) return new Date().toISOString()
              if (typeof raw === 'string') return raw
              if (typeof raw === 'object') {
                const ts = raw as Record<string, unknown>
                const seconds = ts.seconds || ts.Seconds || 0
                const nanos = ts.nanos || ts.Nanos || 0
                const ms = (typeof seconds === 'string' ? parseInt(seconds) : seconds as number) * 1000 + Math.floor((typeof nanos === 'number' ? nanos : 0) / 1000000)
                return ms > 0 ? new Date(ms).toISOString() : new Date().toISOString()
              }
              return String(raw)
            })(),
            isBot: isBotMsg,
          }
        })
        
        const msgMap = new Map<string, Message>()
        
        for (const m of localMsgs) {
          msgMap.set(m.id, m)
        }
        
        for (const m of remoteMsgs) {
          if (msgMap.has(m.id)) {
            const existing = msgMap.get(m.id)!
            // 合并：如果远程消息缺少 mediaUrl/mediaMeta 但本地有，保留本地的
            const merged = {
              ...m,
              mediaUrl: m.mediaUrl || existing.mediaUrl,
              mediaMeta: m.mediaMeta || existing.mediaMeta,
              messageType: (m.messageType === 'text' && existing.messageType !== 'text') ? existing.messageType : m.messageType,
            }
            msgMap.set(m.id, merged)
          } else {
            let replacedLocal = false
            for (const [id, localMsg] of msgMap) {
              if (id.startsWith('local-') && localMsg.senderId === m.senderId && localMsg.content.trim() === m.content.trim()) {
                msgMap.delete(id)
                msgMap.set(m.id, m)
                replacedLocal = true
                break
              }
            }
            if (!replacedLocal) {
              let replacedWs = false
              for (const [id, existingMsg] of msgMap) {
                if (!id.startsWith('local-') && existingMsg.senderId === m.senderId && existingMsg.content.trim() === m.content.trim() && Math.abs(new Date(existingMsg.createdAt).getTime() - new Date(m.createdAt).getTime()) < 60000) {
                  msgMap.set(id, { ...m, id: id })
                  replacedWs = true
                  break
                }
              }
              if (!replacedWs) {
                msgMap.set(m.id, m)
              }
            }
          }
        }
        
        const sorted = Array.from(msgMap.values()).sort((a, b) => {
          const timeA = new Date(a.createdAt).getTime()
          const timeB = new Date(b.createdAt).getTime()
          return timeA - timeB
        })
        
        persistAndSetMessages(chatId, sorted)
      }
    } catch { /* keep local */ }
  }, [persistAndSetMessages, user, getCorrectSenderName, groupMembers])

  useEffect(() => {
    if (selectedChatId) {
      loadChatMessagesFromStore(selectedChatId)
      fetchRemoteMessages(selectedChatId)
      
      // 防抖：确保同一chatId在1秒内只调用一次markMessagesRead
      const now = Date.now()
      const lastCall = lastMarkReadTime.current[selectedChatId] || 0
      if (now - lastCall > 1000) {
        markMessagesRead(selectedChatId).catch(() => {})
        lastMarkReadTime.current[selectedChatId] = now
      }
      
      setChats((prev) => prev.map((c) => c.id === selectedChatId ? { ...c, unreadCount: 0 } : c))
      
      // 如果是群组，获取成员信息和群组信息
      const selectedChat = chatsRef.current.find((c) => c.id === selectedChatId)
      if (selectedChat?.type === 'group') {
        Promise.all([
          getGroupMembers(selectedChatId),
          getGroup(selectedChatId)
        ]).then(([members, groupData]) => {
          const newMap = new Map<string, { userId: string; username: string; avatar: string; role: string }>()
          members.forEach((member: any) => {
            newMap.set(member.userId, { 
              userId: member.userId, 
              username: member.username, 
              avatar: member.avatar,
              role: member.role || 'member'
            })
          })
          setGroupMembers(newMap)
          setGroupInfo(groupData)
        })
      } else {
        setGroupMembers(new Map())
        setGroupInfo(null)
      }
    } else { activeChatIdRef.current = null; setMessages([]) }
    setShowSearchPanel(false)
    setSearchResults([])
    setSearchKeyword('')
    setSearchStartTime('')
    setSearchEndTime('')
    setTypingChatId(null)
  }, [selectedChatId])

  const refreshConversations = useCallback(async () => {
    const startTime = new Date()
    console.log(`%c🔵 [前端] 开始刷新会话列表`, 'background: #00BFFF; color: white; font-size:12px; padding:2px 5px; border-radius:3px;', 
      `time: ${startTime.toISOString()}`)
    
    try {
      const conversations = await getConversationList(1, 100)
      const gotListTime = new Date()
      console.log(`%c🔵 [前端] 从后端获取到会话列表`, 'background: #00BFFF; color: white; font-size:12px; padding:2px 5px; border-radius:3px;', 
        `time: ${gotListTime.toISOString()}, count: ${conversations?.length}, conversations:`, conversations)
      
      if (conversations && Array.isArray(conversations)) {
        // 先获取所有群组的信息
        const groupPromises: Promise<any>[] = []
        const groupIds: string[] = []
        
        for (const conv of conversations) {
          const chatId = String(conv.chat_id || conv.id || '')
          const rawChatType = conv.chat_type || conv.type
          if (rawChatType === 2 || chatId.startsWith('group_') || chatId.startsWith('group-')) {
            groupIds.push(chatId)
            groupPromises.push(getGroup(chatId).catch(() => null))
          }
        }
        
        // 等待所有群组信息获取完成
        const groupInfos = await Promise.all(groupPromises)
        const groupMap = new Map<string, any>()
        groupIds.forEach((id, index) => {
          if (groupInfos[index]) {
            groupMap.set(id, groupInfos[index])
          }
        })
        
        // 获取需要更新的私聊用户信息
        const userPromises: Promise<void>[] = []
        const userMap = new Map<string, any>()
        
        for (const conv of conversations) {
          const chatId = String(conv.chat_id || conv.id || '')
          const rawChatType = conv.chat_type || conv.type
          const isGroupChat = rawChatType === 2 || chatId.startsWith('group_') || chatId.startsWith('group-')
          const isBotChat = rawChatType === 3 || chatId.startsWith('bot_') || chatId.startsWith('bot-')
          
          if (!isGroupChat && !isBotChat) {
            const chatName = conv.name || '新对话'
            const now = new Date()
            console.log(`%c🔵 [前端] 处理私聊会话`, 'background: #87CEEB; color: black; font-size:11px; padding:2px 4px; border-radius:3px;', 
              `time: ${now.toISOString()}, chatId: ${chatId}, 当前name: '${chatName}', 当前avatar: '${conv.avatar || ''}'`)
            
            if (chatName === '新对话' || !chatName) {
              // 从chatId解析出对方用户ID
              let otherUserId: string | null = null
              const parts = chatId.split('_')
              if (parts.length === 3 && parts[0] === 'private') {
                otherUserId = parts[1] === user?.id ? parts[2] : parts[1]
              } else if (parts.length === 2) {
                otherUserId = parts[0] === user?.id ? parts[1] : parts[0]
              }
              
              const now = new Date()
              console.log(`%c🔵 [前端] 解析出对方用户ID`, 'background: #87CEEB; color: black; font-size:11px; padding:2px 4px; border-radius:3px;', 
                `time: ${now.toISOString()}, otherUserId: ${otherUserId}, 当前用户ID: ${user?.id}`)
              
              if (otherUserId) {
                // 前端直接去获取用户信息
                const promise = getUser(otherUserId)
                    .then((userInfo) => {
                      const now = new Date()
                      console.log(`%c🟢 [前端] 获取到对方用户信息`, 'background: #7CFC00; color: black; font-size:11px; padding:2px 4px; border-radius:3px;', 
                        `time: ${now.toISOString()}, otherUserId: ${otherUserId}, userInfo:`, userInfo)
                      if (userInfo) {
                        userMap.set(chatId, userInfo)
                      }
                    })
                    .catch((err) => {
                      const now = new Date()
                      console.log(`%c🔴 [前端] 获取用户信息失败`, 'background: #FF6B6B; color: white; font-size:11px; padding:2px 4px; border-radius:3px;', 
                        `time: ${now.toISOString()}, otherUserId: ${otherUserId}, error:`, err)
                    })
                userPromises.push(promise)
              }
            }
          }
        }
        
        const waitStart = new Date()
        console.log(`%c🔵 [前端] 等待用户信息获取完成`, 'background: #00BFFF; color: white; font-size:12px; padding:2px 5px; border-radius:3px;', 
          `time: ${waitStart.toISOString()}, promise count: ${userPromises.length}`)
        
        // 等待用户信息获取完成
        await Promise.all(userPromises)
        
        const waitDone = new Date()
        console.log(`%c🟢 [前端] 所有用户信息获取完成`, 'background: #7CFC00; color: black; font-size:12px; padding:2px 5px; border-radius:3px;', 
          `time: ${waitDone.toISOString()}, userMap:`, userMap)
        
        const now = new Date()
        console.log(`%c🟣 [前端] 后端返回的完整conversations列表`, 'background: #FF00FF; color: white; font-size:12px; padding:2px 5px; border-radius:3px;', 
          `time: ${now.toISOString()}, data:`, conversations)
        
        setChats((prev) => {
          const existingChats = new Map(prev.map((c) => [c.id, c]))
          
          // 先保存原来的 Bot 聊天
          const botChats: ChatItem[] = prev.filter((c) => c.type === 'bot')
          
          for (const conv of conversations) {
            const chatId = String(conv.chat_id || conv.id || '')
            if (!chatId) continue

            let chatType: 'private' | 'group' | 'bot' = 'private'
            let chatName = conv.name || '新对话'
            let chatAvatar = conv.avatar || ''
            let lastMessage = conv.last_message || ''
            let lastMessageTime = conv.last_message_time || ''
            let unreadCount = conv.unread_count || 0
            let userId = conv.user_id || ''
            let botId = conv.bot_id || ''
            let isFriend = conv.is_friend === true

            if (!userId && chatType === 'private') {
              const parts = chatId.split('_')
              if (parts.length === 3 && parts[0] === 'private') {
                userId = parts[1] === user?.id ? parts[2] : parts[1]
              } else if (parts.length === 2) {
                userId = parts[0] === user?.id ? parts[1] : parts[0]
              }
            }
            
            console.log(`%c🔵 [前端] 单个会话处理`, 'background: #0088FF; color: white; font-size:10px; padding:1px 3px; border-radius:2px;', 
              `chatId: ${chatId}, chatType: ${conv.chat_type}, name: ${chatName}, isFriend: ${conv.is_friend}, typeof isFriend: ${typeof conv.is_friend}`)

            const rawChatType = conv.chat_type || conv.type
            const isGroupChat = rawChatType === 2 || chatId.startsWith('group_') || chatId.startsWith('group-')
            const isBotChat = rawChatType === 3 || chatId.startsWith('bot_') || chatId.startsWith('bot-')

            // 跳过 bot 聊天，避免与 getBotList 创建的 bot 聊天重复
            if (isBotChat) continue

            if (isGroupChat) {
              chatType = 'group'
              const groupInfo = groupMap.get(chatId)
              if (groupInfo) {
                chatName = groupInfo.name || '群组'
                chatAvatar = groupInfo.avatar || ''
              } else if (chatName === '新对话') {
                chatName = '群组'
              }
            }

            // 如果是私聊，名字还是"新对话"，看看前端有没有获取到用户信息
            if (!isGroupChat && !isBotChat && (chatName === '新对话' || !chatName)) {
              const userInfo = userMap.get(chatId)
              const now = new Date()
              console.log(`%c🟡 [前端] 尝试更新会话信息`, 'background: #FFD700; color: black; font-size:11px; padding:2px 4px; border-radius:3px;', 
                `time: ${now.toISOString()}, chatId: ${chatId}, hasUserInfo: ${!!userInfo}, userInfo:`, userInfo)
              
              if (userInfo) {
                chatName = userInfo.username || userInfo.nickname || chatName
                chatAvatar = userInfo.avatar || chatAvatar
                const updateDone = new Date()
                console.log(`%c🟢 [前端] 更新会话信息成功`, 'background: #7CFC00; color: black; font-size:11px; padding:2px 4px; border-radius:3px;', 
                  `time: ${updateDone.toISOString()}, chatId: ${chatId}, newName: '${chatName}', newAvatar: '${chatAvatar}'`)
              }
            }

            const existingChat = existingChats.get(chatId)
            if (existingChat) {
              // 更新现有聊天
              let finalName = existingChat.name
              let finalAvatar = existingChat.avatar
              
              if (!isGroupChat && chatType !== 'bot') {
                if (chatName && chatName !== '新对话') {
                  finalName = chatName
                }
                if (chatAvatar) {
                  finalAvatar = chatAvatar
                }
              }
              // 如果是群组则使用群组信息
              if (isGroupChat && groupMap.get(chatId)?.name) {
                finalName = groupMap.get(chatId)?.name
              }
              if (isGroupChat && groupMap.get(chatId)?.avatar) {
                finalAvatar = groupMap.get(chatId)?.avatar
              }
              
              existingChats.set(chatId, {
                ...existingChat,
                type: chatType,
                name: finalName,
                avatar: finalAvatar,
                lastMessage: lastMessage || existingChat.lastMessage,
                lastMessageTime: lastMessageTime || existingChat.lastMessageTime,
                unreadCount: unreadCount || existingChat.unreadCount,
                isFriend: isFriend,
              })
            } else {
              // 添加新聊天
              existingChats.set(chatId, {
                id: chatId,
                type: chatType,
                name: chatName,
                avatar: chatAvatar,
                lastMessage,
                lastMessageTime,
                unreadCount,
                userId,
                botId,
                isFriend,
              })
            }
          }

          // 构建最终的聊天列表：先放 Bot 聊天，再放其他聊天
          const finalChats: ChatItem[] = [...botChats]
          const existingIds = new Set(botChats.map((c) => c.id))
          
          // 添加其他聊天（不重复）
          for (const chat of existingChats.values()) {
            if (!existingIds.has(chat.id)) {
              finalChats.push(chat)
            }
          }
          
          return finalChats
        })
      }
    } catch {
      // ignore
    }
  }, [user])

  const handleCreateGroup = useCallback(async () => {
    if (!newGroupName.trim()) {
      alert('请输入群组名称')
      return
    }
    
    setCreateGroupLoading(true)
    try {
      const group = await createGroup(newGroupName.trim(), newGroupDescription.trim())
      if (group) {
        // 自动添加群组到聊天列表
        setChats((prev) => {
          const existing = prev.find((c) => c.id === group.id)
          if (!existing) {
            const newChat: ChatItem = {
              id: group.id,
              type: 'group',
              name: group.name,
              avatar: group.avatar,
              unreadCount: 0,
            }
            return [newChat, ...prev]
          }
          return prev
        })
        setShowCreateGroup(false)
        setNewGroupName('')
        setNewGroupDescription('')
        // 选中新创建的群组
        setSelectedChatId(group.id)
      }
    } catch (error) {
      alert('创建群组失败')
    } finally {
      setCreateGroupLoading(false)
    }
  }, [newGroupName, newGroupDescription])

  const handleJoinGroup = async () => {
    if (!joinGroupId.trim()) return
    const gid = joinGroupId.trim()
    setJoinGroupError('')
    setJoinLoading(true)

    const existing = chats.find((c) => c.id === gid)
    if (existing) {
      setSelectedChatId(existing.id)
      setShowJoinGroup(false)
      setJoinGroupId('')
      setJoinLoading(false)
      return
    }

    try {
      await joinGroup(gid)
      const group = await getGroup(gid)
      if (group && group.id) {
        // 添加到聊天列表
        setChats((prev) => {
          const existingChat = prev.find((c) => c.id === group.id)
          if (!existingChat) {
            const newChat: ChatItem = {
              id: group.id,
              type: 'group',
              name: group.name || '群组',
              avatar: group.avatar || '',
              unreadCount: 0,
            }
            return [newChat, ...prev]
          }
          return prev
        })
        setSelectedChatId(group.id)
        setShowJoinGroup(false)
        setJoinGroupId('')
      } else {
        setJoinGroupError('加入群组失败：群组不存在或服务器未返回数据')
      }
    } catch {
      setJoinGroupError('群组不存在或加入失败，请检查群组ID')
    } finally {
      setJoinLoading(false)
    }
  }

  const handleAddMember = async () => {
    if (!selectedChat || !addMemberId.trim()) return
    setAddMemberError('')
    try {
      await inviteGroupMember(selectedChat.id, [addMemberId.trim()])
      // 刷新成员列表
      const members = await getGroupMembers(selectedChat.id)
      const newMap = new Map<string, { userId: string; username: string; avatar: string }>()
      members.forEach((member) => {
        newMap.set(member.userId, { userId: member.userId, username: member.username, avatar: member.avatar })
      })
      setGroupMembers(newMap)
      
      setShowAddMember(false)
      setAddMemberId('')
    } catch {
      setAddMemberError('邀请失败，请检查用户ID是否正确')
    }
  }

  const handleRemoveMember = async (userId: string) => {
    if (!selectedChat) return
    try { 
      await kickGroupMember(selectedChat.id, userId)
      // 刷新成员列表
      const members = await getGroupMembers(selectedChat.id)
      const newMap = new Map<string, { userId: string; username: string; avatar: string }>()
      members.forEach((member) => {
        newMap.set(member.userId, { userId: member.userId, username: member.username, avatar: member.avatar })
      })
      setGroupMembers(newMap)
    } catch { /* */ }
  }

  const handleTransferOwner = async () => {
    if (!selectedChat || !transferToUserId) return
    try {
      await transferGroupOwner(selectedChat.id, transferToUserId)
      setShowTransferOwner(false)
      setTransferToUserId('')
      const members = await getGroupMembers(selectedChat.id)
      const newMap = new Map<string, { userId: string; username: string; avatar: string; role: string }>()
      members.forEach((member: any) => {
        newMap.set(member.userId, {
          userId: member.userId,
          username: member.username,
          avatar: member.avatar,
          role: member.role || 'member'
        })
      })
      setGroupMembers(newMap)
      const groupData = await getGroup(selectedChat.id)
      setGroupInfo(groupData)
    } catch {
      alert('转让群主失败')
    }
  }

  const handleMuteMember = async () => {
    if (!selectedChat || !muteMemberId) return
    try {
      await muteGroupMember(selectedChat.id, muteMemberId, muteDuration)
      setShowMuteMember(false)
      setMuteMemberId('')
      setMuteDuration(0)
      const members = await getGroupMembers(selectedChat.id)
      const newMap = new Map<string, { userId: string; username: string; avatar: string; role: string }>()
      members.forEach((member: any) => {
        newMap.set(member.userId, {
          userId: member.userId,
          username: member.username,
          avatar: member.avatar,
          role: member.role || 'member'
        })
      })
      setGroupMembers(newMap)
    } catch {
      alert('禁言操作失败')
    }
  }

  const handleUpdateAnnouncement = async () => {
    if (!selectedChat) return
    try {
      await updateGroupAnnouncement(selectedChat.id, announcementText)
      const groupData = await getGroup(selectedChat.id)
      setGroupInfo(groupData)
      setShowAnnouncement(false)
    } catch {
      alert('更新公告失败')
    }
  }

  const handleSetGroupAdmin = async () => {
    if (!selectedChat || !setAdminMemberId) return
    try {
      await setGroupAdmin(selectedChat.id, setAdminMemberId, setAdminIsAdmin)
      setShowSetAdmin(false)
      setSetAdminMemberId('')
      setSetAdminIsAdmin(false)
      const members = await getGroupMembers(selectedChat.id)
      const newMap = new Map<string, { userId: string; username: string; avatar: string; role: string }>()
      members.forEach((member: any) => {
        newMap.set(member.userId, {
          userId: member.userId,
          username: member.username,
          avatar: member.avatar,
          role: member.role || 'member'
        })
      })
      setGroupMembers(newMap)
    } catch {
      alert('设置管理员失败')
    }
  }

  const handleInviteBot = async () => {
    if (!selectedChat || !inviteBotId.trim()) return
    try {
      await inviteBotToGroup(selectedChat.id, inviteBotId.trim())
      setShowInviteBot(false)
      setInviteBotId('')
      const members = await getGroupMembers(selectedChat.id)
      const newMap = new Map<string, { userId: string; username: string; avatar: string; role: string }>()
      members.forEach((member: any) => {
        newMap.set(member.userId, {
          userId: member.userId,
          username: member.username,
          avatar: member.avatar,
          role: member.role || 'member'
        })
      })
      setGroupMembers(newMap)
      const groupData = await getGroup(selectedChat.id)
      setGroupInfo(groupData)
    } catch {
      alert('邀请Bot失败')
    }
  }

  const roleIcon = (role: string) => {
    if (role === 'owner') return <Crown size={14} className="role-owner" />
    if (role === 'admin') return <Shield size={14} className="role-admin" />
    return null
  }

  const isOwner = user && groupInfo && user.id === groupInfo.owner_id
  const isAdmin = user && groupMembers.get(String(user.id))?.role === 'admin'

  const groupBots = useMemo(() => {
    if (selectedChat?.type !== 'group') return []
    return Array.from(groupMembers.values())
      .filter((m: any) => m.userId.startsWith('bot_'))
      .map((m: any) => {
        const botId = m.userId.replace('bot_', '')
        const botInfo = bots.find(b => b.id === botId)
        return {
          id: botId,
          name: botInfo?.name || m.username || 'Bot',
          avatar: botInfo?.avatar || m.avatar || '',
        }
      })
  }, [groupMembers, bots, selectedChat])

  const toggleGroupCollapse = (groupName: string) => {
    setCollapsedGroups(prev => {
      const newSet = new Set(prev)
      if (newSet.has(groupName)) {
        newSet.delete(groupName)
      } else {
        newSet.add(groupName)
      }
      return newSet
    })
  }

  useEffect(() => {
    let mounted = true
    getBotList().then((list) => {
      if (!mounted) return
      const safeList = Array.isArray(list) ? list : []
      console.log('[BotList] 获取到的 bots:', safeList.map((b) => ({ id: b.id, name: b.name, avatar: b.avatar })))
      setBots(safeList)
      setChats((prev) => {
        // 保留本地已有的 bot avatar
        const existingBotAvatars = new Map<string, string>()
        prev.filter((c) => c.type === 'bot').forEach((c) => {
          if (c.avatar) existingBotAvatars.set(c.botId || c.id.replace('bot-', ''), c.avatar)
        })
        const botChats: ChatItem[] = safeList.map((b) => {
          const avatar = b.avatar || existingBotAvatars.get(b.id) || ''
          return { id: `bot-${b.id}`, type: 'bot' as const, name: b.name, avatar, unreadCount: 0, botId: b.id }
        })
        return [...botChats, ...prev.filter((c) => c.type !== 'bot')]
      })
    }).catch(() => {})
    
    refreshConversations()
    
    // 延迟500ms再刷新一次，等后端异步获取用户信息
    const timer = setTimeout(() => {
      if (mounted) {
        refreshConversations()
      }
    }, 500)
    
    return () => {
      mounted = false
      clearTimeout(timer)
    }
  }, []) // 只在组件挂载时执行一次

  const processWsMessage = useCallback((msg: WsMessage) => {
    if (msg.type !== 'message') return
    const data = msg.data as Record<string, unknown>

    // 🟣 🔴 前端收到消息
    const recvTime = new Date()
    console.log(`%c🟣 🔴 [前端] 收到私聊消息`, 'background: #9370DB; color: white; font-size:12px; padding:2px 5px; border-radius:3px;', 
      `time: ${recvTime.toISOString()}, msgId: ${String(data.id || '')}`)

    const chatId = String(data.chat_id || data.chatId || '')
    const senderId = String(data.sender_id || data.senderId || '')

    const serverSenderName = String(data.sender_name || data.senderName || '')
    const senderName = serverSenderName || getCorrectSenderName(senderId, chatId)
    
    // 从groupMembers中获取头像
    let senderAvatar = String(data.sender_avatar || data.senderAvatar || '')
    if (!senderAvatar && groupMembers.has(senderId)) {
      senderAvatar = groupMembers.get(senderId)!.avatar
    }
    const isSelfEcho = senderId === user?.id
    if (!senderAvatar && !isSelfEcho) {
      const chatItem = chatsRef.current.find(c => c.id === chatId)
      if (chatItem?.type === 'private' && chatItem.avatar) {
        senderAvatar = chatItem.avatar
      }
    }
    if (isSelfEcho && !senderAvatar && user?.avatar) {
      senderAvatar = user.avatar
    }

    // 判断是否为系统消息
    const resolvedMsgType = resolveMessageType(data.message_type || data.messageType)

    const rawChatType = data.chat_type || 1
    const isGroupChat = rawChatType === 2 || chatId.startsWith('group_') || chatId.startsWith('group-')
    const isBotChat = rawChatType === 3 || chatId.startsWith('bot_') || chatId.startsWith('bot-')

    // 如果是 Bot 消息，补充 Bot 的头像和名称
    let finalSenderName = senderName
    let finalSenderAvatar = senderAvatar
    let finalIsBot = !!data.is_bot || !!data.isBot

    if (isBotChat && !isSelfEcho) {
      const botId = senderId || chatId.replace('bot-', '')
      const botInfo = bots.find((b) => b.id === botId)
      if (botInfo) {
        finalSenderName = botInfo.name || finalSenderName || 'Bot'
        finalSenderAvatar = botInfo.avatar || finalSenderAvatar
        finalIsBot = true
      }
    }

    const incoming: Message = {
      id: String(data.id || ''),
      chatId: chatId,
      senderId: senderId,
      senderName: finalSenderName,
      senderAvatar: finalSenderAvatar,
      content: String(data.content || '').trim(),
      messageType: resolvedMsgType,
      mediaUrl: toMediaUrl(String(data.media_url || data.mediaUrl || '')),
      mediaMeta: resolveMediaMeta(data.media_meta || data.mediaMeta),
      createdAt: String(data.created_at || data.createdAt || data.timestamp || new Date().toISOString()),
      isBot: finalIsBot
    }

    if (!incoming || !incoming.chatId || !incoming.id) return

    // 如果是私聊，收到消息后刷新会话列表，获取正确的对方名字
    if (!isGroupChat && !isBotChat) {
      setTimeout(() => {
        refreshConversations()
      }, 300)
    }

    // 如果是群组消息，异步获取群组信息，不阻塞消息显示
    if (isGroupChat) {
      getGroup(chatId).then((g) => {
        setChats(prev => prev.map(c => c.id === chatId ? { ...c, name: g?.name || c.name, avatar: g?.avatar || c.avatar } : c))
      }).catch(() => {})
    }

    // 先更新聊天列表，不等待群组信息获取
    setChats((prev) => {
      const existing = prev.find((c) => c.id === chatId)
      if (!existing) {
        let chatType: 'private' | 'group' | 'bot' = 'private'
        let chatName = '新对话' // 先用默认名称，等刷新会话列表时再获取正确的
        let chatAvatar = ''
        let targetUserId = senderId

        // 如果是自己发的消息，私聊会话，对方是另一个用户
        if (!isGroupChat && !isBotChat && isSelfEcho) {
          // 从 chatId 找出对方用户 ID
          const parts = chatId.split('_')
          if (parts.length === 3 && parts[0] === 'private') {
            targetUserId = parts[1] === user?.id ? parts[2] : parts[1]
          } else if (parts.length === 2) {
            targetUserId = parts[0] === user?.id ? parts[1] : parts[0]
          }
        }

        if (isGroupChat) {
          chatType = 'group'
          chatName = '群组' // 先用默认名称，后台更新
          chatAvatar = ''
        } else if (isBotChat) {
          chatType = 'bot'
        }

        const newChat: ChatItem = {
          id: chatId,
          type: chatType,
          name: chatName,
          avatar: chatAvatar,
          unreadCount: isSelfEcho ? 0 : (chatId !== selectedChatId ? 1 : 0),
          userId: targetUserId,
          lastMessage: incoming.content,
          lastMessageTime: incoming.createdAt
        }
        return [newChat, ...prev]
      }
      return prev.map((c) => {
        if (c.id === chatId) {
          // 私聊不要用 senderName 覆盖会话名字，会话名字应该是对方的名字
          // 等 refreshConversations() 时从后端获取正确的名字
          return {
            ...c,
            type: isGroupChat ? 'group' : isBotChat ? 'bot' : c.type,
            name: c.name,
            avatar: c.avatar,
            lastMessage: incoming.content,
            lastMessageTime: incoming.createdAt,
            unreadCount: isSelfEcho ? c.unreadCount : (chatId !== selectedChatId ? (c.unreadCount || 0) + 1 : 0)
          }
        }
        return c
      })
    })

    // 先检查并记录需要替换的信息，不要先删除 sentKey！
    let needToReplace: { sentKey: string, localId: string } | null = null
    if (isSelfEcho) {
      // 先尝试普通文本消息的 key
      let sentKey = `${chatId}:${incoming.content.trim()}`
      let sentInfo = sentMessagesRef.current.get(sentKey)
      
      console.log('%c📥 [WS] 检查去重', 'background: #2196F3; color: white; padding:2px 5px; border-radius:3px;', {
        incomingId: incoming.id,
        incomingContent: `"${incoming.content.trim()}"`,
        incomingMediaMeta: incoming.mediaMeta,
        incomingMediaType: incoming.messageType,
        incomingMediaUrl: incoming.mediaUrl,
        textKey: sentKey,
        textKeyFound: !!sentInfo,
        allSentKeys: [...sentMessagesRef.current.keys()],
      })
      
      // 如果没找到，尝试媒体消息的 key
      if (!sentInfo && incoming.mediaMeta) {
        sentKey = `${chatId}:${incoming.content.trim()}:${incoming.mediaMeta}`
        sentInfo = sentMessagesRef.current.get(sentKey)
        console.log('%c📥 [WS] 媒体key查找', 'background: #2196F3; color: white; padding:2px 5px; border-radius:3px;', {
          mediaKey: sentKey,
          mediaKeyFound: !!sentInfo,
        })
      }
      
      // 如果还是没找到，尝试用 mediaUrl 匹配（服务器可能不返回 mediaMeta）
      if (!sentInfo && incoming.mediaUrl) {
        const rawMediaUrl = String(data.media_url || data.mediaUrl || '')
        const matchedLocalId = mediaUrlToLocalIdRef.current.get(rawMediaUrl)
        console.log('%c📥 [WS] mediaUrl匹配查找', 'background: #2196F3; color: white; padding:2px 5px; border-radius:3px;', {
          rawMediaUrl,
          matchedLocalId,
          allMediaMappings: [...mediaUrlToLocalIdRef.current.entries()],
        })
        if (matchedLocalId) {
          sentInfo = { localId: matchedLocalId, timestamp: Date.now() }
          sentKey = `mediaUrl:${rawMediaUrl}`
        }
      }
      
      if (sentInfo) {
        needToReplace = { sentKey, localId: sentInfo.localId }
        console.log('%c📥 [WS] 找到匹配！', 'background: #4CAF50; color: white; padding:2px 5px; border-radius:3px;', {
          sentKey, localId: sentInfo.localId
        })
      } else {
        console.log('%c📥 [WS] 未找到匹配', 'background: #f44336; color: white; padding:2px 5px; border-radius:3px;', {
          isSelfEcho, content: `"${incoming.content.trim()}"`, mediaMeta: incoming.mediaMeta, mediaUrl: incoming.mediaUrl
        })
      }
    }

    // 立即更新 UI 显示消息！不等待持久化！
    if (activeChatIdRef.current === chatId) {
      setMessages((prevMsgs) => {
        // 简单的去重：检查是否已有此ID的消息
        const existingMsg = prevMsgs.find(m => m.id === incoming.id)
        if (existingMsg) {
          return prevMsgs
        }

        // Bot 消息去重：如果已有相同发送者和内容的 Bot 消息在 30 秒内，跳过
        if (incoming.isBot || incoming.senderId.startsWith('bot_')) {
          const duplicateBot = prevMsgs.find(m =>
            (m.isBot || m.senderId.startsWith('bot_')) &&
            m.senderId === incoming.senderId &&
            m.content.trim() === incoming.content.trim() &&
            Math.abs(new Date(m.createdAt).getTime() - new Date(incoming.createdAt).getTime()) < 30000
          )
          if (duplicateBot) {
            return prevMsgs
          }
        }
        
        // 对于发送方，检查是否有本地临时消息需要替换
        if (needToReplace) {
          const localIdx = prevMsgs.findIndex((m) => m.id === needToReplace.localId)
          if (localIdx !== -1) {
            const updated = prevMsgs.map((m) => m.id === needToReplace.localId ? incoming : m)
            const sorted = updated.sort((a, b) => new Date(a.createdAt).getTime() - new Date(b.createdAt).getTime())
            return sorted
          }
        }
        
        // 直接添加新消息并排序
        const updated = [...prevMsgs, incoming]
        const sorted = updated.sort((a, b) => new Date(a.createdAt).getTime() - new Date(b.createdAt).getTime())
        return sorted
      })
    }

    // 后台异步持久化！不阻塞 UI！
    queueMicrotask(() => {
      const currentMsgs = loadChatMessages(chatId)
      
      // 简单的去重：检查是否已有此ID的消息
      const existingMsg = currentMsgs.find(m => m.id === incoming.id)
      if (existingMsg) {
        console.log('%c💾 [持久化] 消息已存在，跳过', 'background: #9C27B0; color: white; padding:2px 5px; border-radius:3px;', {
          incomingId: incoming.id, needToReplace: needToReplace?.sentKey
        })
        if (needToReplace) {
          sentMessagesRef.current.delete(needToReplace.sentKey)
          if (needToReplace.sentKey.startsWith('mediaUrl:')) {
            mediaUrlToLocalIdRef.current.delete(needToReplace.sentKey.replace('mediaUrl:', ''))
          }
        }
        return
      }

      // Bot 消息持久化去重：如果已有相同内容的 Bot 消息在 10 秒内，跳过
      if (incoming.isBot) {
        const duplicateBot = currentMsgs.find(m =>
          m.isBot &&
          m.content.trim() === incoming.content.trim() &&
          Math.abs(new Date(m.createdAt).getTime() - new Date(incoming.createdAt).getTime()) < 10000
        )
        if (duplicateBot) {
          console.log('%c💾 [持久化] Bot消息重复，跳过', 'background: #9C27B0; color: white; padding:2px 5px; border-radius:3px;', {
            incomingId: incoming.id, content: incoming.content.trim().slice(0, 30)
          })
          return
        }
      }
      
      // 对于发送方，检查是否有本地临时消息需要替换
      if (needToReplace) {
        const localIdx = currentMsgs.findIndex((m) => m.id === needToReplace.localId)
        console.log('%c💾 [持久化] 替换本地消息', 'background: #9C27B0; color: white; padding:2px 5px; border-radius:3px;', {
          localId: needToReplace.localId, found: localIdx !== -1,
          incomingMediaUrl: incoming.mediaUrl
        })
        if (localIdx !== -1) {
          const updated = currentMsgs.map((m) => m.id === needToReplace.localId ? incoming : m)
          const sorted = updated.sort((a, b) => new Date(a.createdAt).getTime() - new Date(b.createdAt).getTime())
          persistAndSetMessages(chatId, sorted)
          sentMessagesRef.current.delete(needToReplace.sentKey)
          if (needToReplace.sentKey.startsWith('mediaUrl:')) {
            mediaUrlToLocalIdRef.current.delete(needToReplace.sentKey.replace('mediaUrl:', ''))
          }
          console.log('%c💾 [持久化] 替换成功！', 'background: #4CAF50; color: white; padding:2px 5px; border-radius:3px;', {
            mediaUrl: incoming.mediaUrl
          })
          return
        }
      }
      
      // 直接添加新消息并排序（带去重）
      const alreadyExists = currentMsgs.some(m => m.id === incoming.id)
      const botDuplicate = (incoming.isBot || incoming.senderId.startsWith('bot_')) &&
        currentMsgs.some(m =>
          (m.isBot || m.senderId.startsWith('bot_')) &&
          m.senderId === incoming.senderId &&
          m.content.trim() === incoming.content.trim() &&
          Math.abs(new Date(m.createdAt).getTime() - new Date(incoming.createdAt).getTime()) < 30000
        )
      if (alreadyExists || botDuplicate) {
        console.log('%c💾 [持久化] 跳过重复消息', 'background: #FF9800; color: white; padding:2px 5px; border-radius:3px;', { incomingId: incoming.id })
        return
      }
      const updated = [...currentMsgs, incoming]
      const sorted = updated.sort((a, b) => new Date(a.createdAt).getTime() - new Date(b.createdAt).getTime())
      persistAndSetMessages(chatId, sorted)
      console.log('%c💾 [持久化] 直接添加', 'background: #9C27B0; color: white; padding:2px 5px; border-radius:3px;', {
        incomingId: incoming.id, mediaUrl: incoming.mediaUrl, needToReplace: !!needToReplace
      })
      if (needToReplace) {
        sentMessagesRef.current.delete(needToReplace.sentKey)
        if (needToReplace.sentKey.startsWith('mediaUrl:')) {
          mediaUrlToLocalIdRef.current.delete(needToReplace.sentKey.replace('mediaUrl:', ''))
        }
      }
    })
  }, [selectedChatId, user, getCorrectSenderName, groupMembers])

  const handleWsMessage = useCallback((msg: WsMessage) => {
    if (msg.type === 'withdraw') {
      const data = msg.data as Record<string, unknown>
      const messageId = String(data.message_id || '')
      const chatId = String(data.chat_id || '')
      if (!messageId || !chatId) return

      setMessages((prevMsgs) => {
        const updated = prevMsgs.map((m) => {
          if (m.id === messageId) {
            return { ...m, content: '[消息已撤回]', messageType: 'withdrawn' as const }
          }
          return m
        })
        return updated
      })

      if (activeChatIdRef.current === chatId) {
        queueMicrotask(() => {
          const currentMsgs = loadChatMessages(chatId)
          const updated = currentMsgs.map((m) => {
            if (m.id === messageId) {
              return { ...m, content: '[消息已撤回]', messageType: 'withdrawn' as const }
            }
            return m
          })
          persistAndSetMessages(chatId, updated)
        })
      }

      setChats((prevChats) => prevChats.map((chat) => {
        if (chat.id === chatId) {
          return { ...chat, lastMessage: '[消息已撤回]' }
        }
        return chat
      }))

      return
    }

    if (msg.type === 'typing') {
      const data = msg.data as Record<string, unknown>
      const chatId = String(data.chat_id || '')
      const isTyping = !!data.typing
      if (!isTyping) {
        if (typingChatId === chatId) setTypingChatId(null)
        return
      }
      if (chatId === selectedChatId) {
        setTypingChatId(chatId)
        if (typingTimerRef.current) clearTimeout(typingTimerRef.current)
        typingTimerRef.current = setTimeout(() => {
          setTypingChatId(null)
          typingTimerRef.current = null
        }, 3000)
      }
      return
    }

    if (msg.type === 'online_status') {
      const data = msg.data as Record<string, unknown>
      const userId = String(data.user_id || '')
      const online = !!data.online
      if (userId) {
        setOnlineStatusMap((prev) => {
          const next = new Map(prev)
          next.set(userId, online)
          return next
        })
      }
      return
    }

    if (msg.type === 'read_receipt') {
      const data = msg.data as Record<string, unknown>
      const chatId = String(data.chat_id || '')
      const messageIds = (data.message_ids || []) as string[]
      if (!chatId || messageIds.length === 0) return

      const idSet = new Set(messageIds)
      setMessages((prev) => prev.map((m) => {
        if (idSet.has(m.id) && m.senderId === user?.id) {
          return { ...m, isRead: true }
        }
        return m
      }))

      if (activeChatIdRef.current === chatId) {
        queueMicrotask(() => {
          const currentMsgs = loadChatMessages(chatId)
          const updated = currentMsgs.map((m) => {
            if (idSet.has(m.id) && m.senderId === user?.id) {
              return { ...m, isRead: true }
            }
            return m
          })
          persistAndSetMessages(chatId, updated)
        })
      }
      return
    }

    if (msg.type !== 'message') return
    processWsMessage(msg)
  }, [processWsMessage, persistAndSetMessages, selectedChatId, typingChatId, user])

  const { send: wsSend, connected: wsConnected } = useWebSocket(wsBase, { onMessage: handleWsMessage })

  const updateChatLastMessage = (chatId: string, content: string) => {
    setChats((prev) => prev.map((c) => c.id === chatId ? { ...c, lastMessage: content, lastMessageTime: new Date().toISOString() } : c))
  }

  const handleGroupAvatarUpload = async (file: File) => {
    if (!selectedChatId) return
    try {
      const url = await updateGroupAvatar(selectedChatId, file)
      if (url) {
        setChats((prev) => prev.map((c) => c.id === selectedChatId ? { ...c, avatar: url } : c))
      }
    } catch {
      alert('上传群头像失败')
    }
    if (avatarRef.current) avatarRef.current.value = ''
  }

  const handleSummarize = async () => {
    if (!selectedChatId) return
    setShowMenu(false)
    setSummaryLoading(true)
    setShowSummary(true)
    setSummaryPanelType('summary')
    setSummary('')
    setSummaryKeyPoints([])
    setSummaryTodos([])
    setSummaryCandidates([])
    setSummaryParticipants([])
    try {
      const chatType = selectedChat?.type === 'group' ? 2 : 1
      const settings = getAISettings()
      const res = await client.post('/summary/summarize', { chat_id: selectedChatId, chat_type: chatType, include_todos: true, include_candidates: true, message_count: summaryMessageCount, model_config: settings.summary })
      const d = res.data as Record<string, unknown>
      const inner = (d.data || d) as Record<string, unknown>
      setSummary(String(inner.summary || inner.message || '无法生成摘要'))
      setSummaryKeyPoints(Array.isArray(inner.key_points) ? inner.key_points as string[] : [])
      setSummaryTodos(Array.isArray(inner.todos) ? inner.todos as any[] : [])
      setSummaryCandidates(Array.isArray(inner.reply_candidates) ? inner.reply_candidates as any[] : [])
      setSummaryParticipants(Array.isArray(inner.participants) ? inner.participants as string[] : [])
    } catch {
      setSummary('摘要生成失败，请稍后重试')
    } finally {
      setSummaryLoading(false)
    }
  }

  const handleGenerateReplies = async () => {
    if (!selectedChatId || replyLoading) return
    setReplyLoading(true)
    setShowReplyCandidates(true)
    setReplyCandidates([])
    try {
      const chatType = selectedChat?.type === 'group' ? 2 : 1
      const settings = getAISettings()
      const res = await client.post('/summary/reply-candidates', {
        chat_id: selectedChatId,
        chat_type: chatType,
        candidate_count: 3,
        tone: 'friendly',
        model_config: settings.reply,
      })
      const d = res.data as Record<string, unknown>
      const candidates = Array.isArray(d.data) ? d.data : []
      setReplyCandidates(candidates)
    } catch {
      setReplyCandidates([])
    } finally {
      setReplyLoading(false)
    }
  }

  const handleSelectReply = async (content: string) => {
    setShowReplyCandidates(false)
    if (!content.trim() || !selectedChatId) return
    await handleSend(content.trim())
  }

  const handleMentionBot = (botId: string, botName: string) => {
    setPendingMentionIds(prev => prev.includes(`bot_${botId}`) ? prev : [...prev, `bot_${botId}`])
  }

  const handleEditMessage = (message: Message) => {
    setEditingMessage(message)
    setEditContent(message.content)
    setShowEditModal(true)
  }

  const handleSaveEdit = async () => {
    if (!editingMessage || !selectedChatId) return
    try {
      await editMessage(editingMessage.id, editContent)
      
      // 更新当前聊天的消息列表
      const updatedMessages = messages.map(m => 
        m.id === editingMessage.id 
          ? { ...m, content: editContent, editedAt: new Date().toISOString() } 
          : m
      )
      persistAndSetMessages(selectedChatId, updatedMessages)
      
      // 更新会话列表，如果这条消息是该会话的最新一条
      setChats(prevChats => prevChats.map(chat => {
        if (chat.id === selectedChatId) {
          // 获取当前聊天的最新消息
          const latestMsg = updatedMessages.length > 0 
            ? updatedMessages[updatedMessages.length - 1] 
            : null
          
          // 如果编辑的就是最新消息，或者没有最新消息但刚编辑了，就更新
          if (latestMsg && (latestMsg.id === editingMessage.id || !chat.lastMessage)) {
            return {
              ...chat,
              lastMessage: editContent,
              lastMessageTime: new Date().toISOString()
            }
          }
        }
        return chat
      }))
      
      setShowEditModal(false)
      setEditingMessage(null)
    } catch {
      alert('编辑失败')
    }
  }

  const handleWithdrawMessage = async (messageId: string) => {
    if (!selectedChatId) return
    try {
      await withdrawMessage(messageId)
      
      const updated = messages.map(m => 
        m.id === messageId 
          ? { ...m, content: '[消息已撤回]', messageType: 'withdrawn' as const } 
          : m
      )
      persistAndSetMessages(selectedChatId, updated)
      
      const wasLatest = messages.length > 0 && messages[messages.length - 1].id === messageId
      if (wasLatest) {
        setChats(prevChats => prevChats.map(chat => {
          if (chat.id === selectedChatId) {
            return {
              ...chat,
              lastMessage: '[消息已撤回]',
            }
          }
          return chat
        }))
      }
    } catch (error: any) {
      const errorMsg = error?.response?.data?.message || error?.message || '撤回失败'
      alert(errorMsg)
    }
  }

  const handleForwardMessage = (message: Message) => {
    // 检查是否是本地临时消息
    if (message.id.startsWith('local-')) {
      alert('请等待消息发送完成后再转发')
      return
    }
    setForwardingMessage(message)
    setForwardChatId(null)
    setForwardChatType('private')
    setShowForwardModal(true)
  }

  const handleConfirmForward = async () => {
    if (!forwardingMessage || !forwardChatId) {
      return
    }
    
    // 再次检查，防止用户绕过检查
    if (forwardingMessage.id.startsWith('local-')) {
      alert('请等待消息发送完成后再转发')
      return
    }

    // 立即关闭Modal，给用户即时反馈
    setShowForwardModal(false)
    setForwardingMessage(null)

    // 后台发送API请求
    try {
      await forwardMessage(forwardingMessage.id, [forwardChatId], forwardChatType === 'group' ? 2 : 1)
    } catch (error) {
      alert('转发失败')
    }
  }

  const handleDeleteChat = async () => {
    if (!selectedChat) return
    if (!window.confirm(`确定要删除与 ${selectedChat.name} 的聊天记录吗？`)) {
      return
    }
    
    try {
      // 如果是群聊，先退出群组
      if (selectedChat.type === 'group') {
        await leaveGroup(selectedChat.id)
      } else {
        // 对于私聊，删除聊天
        const chatType = 1
        await deleteChat(selectedChat.id, chatType)
      }
      
      // 从列表中移除
      setChats(prev => prev.filter(c => c.id !== selectedChat.id))
      // 清空当前选中
      setSelectedChatId(null)
      setShowMenu(false)
      
      // 清除本地缓存的消息
      const allMsgs = loadAllMessages()
      delete allMsgs[selectedChat.id]
      save('messages', allMsgs)
    } catch (error: any) {
      const errorMsg = error?.response?.data?.message || error?.message || '删除失败'
      alert(errorMsg)
    }
  }

  const handleDeleteFriend = async () => {
    if (!selectedChat) return
    if (selectedChat.type !== 'private' || !selectedChat.userId) {
      alert('只能删除好友关系')
      return
    }
    if (!window.confirm(`确定要删除与 ${selectedChat.name} 的好友关系吗？`)) {
      return
    }
    
    try {
      await deleteFriend(selectedChat.userId)
      setChats(prev => prev.map(c => 
        c.id === selectedChat.id ? { ...c, isFriend: false } : c
      ))
      setShowMenu(false)
      refreshConversations()
      alert('已删除好友关系')
    } catch (error: any) {
      const errorMsg = error?.response?.data?.message || error?.message || '删除失败'
      alert(errorMsg)
    }
  }

  const handleAddFriend = async () => {
    if (!selectedChat) return
    if (selectedChat.type !== 'private' || !selectedChat.userId) {
      alert('只能添加私聊用户为好友')
      return
    }
    
    try {
      await addFriend(selectedChat.userId, '', `我是 ${selectedChat.name}`)
      setChats(prev => prev.map(c => 
        c.id === selectedChat.id ? { ...c, isFriend: true } : c
      ))
      setShowMenu(false)
      refreshConversations()
      alert('好友申请已发送')
    } catch (error: any) {
      const errorMsg = error?.response?.data?.message || error?.message || '添加好友失败'
      alert(errorMsg)
    }
  }

  const handleDeleteChatHistory = async () => {
    if (!selectedChat) return
    if (!window.confirm(`确定要清理 ${selectedChat.name} 的聊天记录吗？`)) {
      return
    }
    
    try {
      await deleteChatHistory(selectedChat.id)
      // 清空当前显示的消息
      setMessages([])
      // 清除本地缓存的消息
      const allMsgs = loadAllMessages()
      delete allMsgs[selectedChat.id]
      save('messages', allMsgs)
      // 更新会话列表的最后消息
      setChats(prev => prev.map(c => 
        c.id === selectedChat.id ? { ...c, lastMessage: '', lastMessageTime: '' } : c
      ))
      setShowMenu(false)
      alert('聊天记录已清理')
    } catch (error: any) {
      const errorMsg = error?.response?.data?.message || error?.message || '清理失败'
      alert(errorMsg)
    }
  }

  const handleSend = async (content: string) => {
    if (!selectedChat) return
    const trimContent = content.trim()
    if (!trimContent) return
    const chatId = selectedChat.id
    const localId = `local-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`
    
    const sendTime = new Date()
    console.log(`%c🟡 🔴 [前端] 发送私聊消息`, 'background: #FFD700; color: black; font-size:12px; padding:2px 5px; border-radius:3px;', 
      `time: ${sendTime.toISOString()}, chatId: ${chatId}, content: ${trimContent}`)
    const localMsg: Message = { id: localId, chatId, senderId: user?.id || '', senderName: user?.nickname || user?.username || '我', senderAvatar: user?.avatar || '', content: trimContent, messageType: 'text', createdAt: new Date().toISOString() }
    sentMessagesRef.current.set(`${chatId}:${trimContent}`, { localId, timestamp: Date.now() })
    
    setMessages((prev) => [...prev, localMsg])
    updateChatLastMessage(chatId, trimContent)
    
    queueMicrotask(() => {
      const currentMsgs = loadChatMessages(chatId)
      const existingIds = new Set(currentMsgs.map(m => m.id))
      if (!existingIds.has(localMsg.id)) {
        persistAndSetMessages(chatId, [...currentMsgs, localMsg])
      }
    })
    
    if (atBotMode || selectedChat.type === 'bot') {
      setAtBotMode(false)
      try {
        console.log('调用 sendBotMessage...', { botId: selectedChat.botId || selectedChat.id, content: trimContent, chatId, selectedChatAvatar: selectedChat.avatar, botsCount: bots.length, botsAvatars: bots.map((b) => ({ id: b.id, avatar: b.avatar })) })
        const result = await sendBotMessage(selectedChat.botId || selectedChat.id, trimContent, chatId, autoSaveToKb)
        console.log('sendBotMessage 结果:', result)

        if (result.error) {
          const errMsg: Message = { id: `bot-err-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`, chatId, senderId: 'system', senderName: '系统', senderAvatar: '', content: `⚠️ ${result.error}`, messageType: 'text', isBot: true, createdAt: new Date().toISOString() }
          setMessages((prev) => [...prev, errMsg])
          updateChatLastMessage(chatId, result.error)
          queueMicrotask(() => {
            const currentMsgs = loadChatMessages(chatId)
            const existingIds = new Set(currentMsgs.map(m => m.id))
            const newMsgs = [localMsg, errMsg].filter(m => !existingIds.has(m.id))
            if (newMsgs.length > 0) persistAndSetMessages(chatId, [...currentMsgs, ...newMsgs])
          })
          return
        }

        const reply = result.content || '(Bot 暂无回复)'
        const displayChatId = selectedChat.id
        const botInfo = bots.find((b) => b.id === (selectedChat.botId || selectedChat.id.replace('bot-', '')))
        const botAvatar = botInfo?.avatar || selectedChat.avatar || ''
        const botName = botInfo?.name || selectedChat.name || 'Bot'
        const botMsg: Message = {
          id: `bot-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
          chatId: displayChatId,
          senderId: selectedChat.botId || selectedChat.id.replace('bot-', ''),
          senderName: botName,
          senderAvatar: botAvatar,
          content: reply,
          messageType: 'text',
          isBot: true,
          createdAt: new Date().toISOString(),
        }

        if (activeChatIdRef.current === displayChatId) {
          setMessages((prev) => {
            const exists = prev.some(m => m.isBot && m.content.trim() === reply.trim() && Math.abs(new Date(m.createdAt).getTime() - Date.now()) < 10000)
            if (exists) return prev
            return [...prev, botMsg]
          })
        }
        updateChatLastMessage(displayChatId, reply)

        queueMicrotask(() => {
          const currentMsgs = loadChatMessages(displayChatId)
          const existingIds = new Set(currentMsgs.map(m => m.id))
          const newMsgs = [localMsg, botMsg].filter(m => !existingIds.has(m.id))
          if (newMsgs.length > 0) persistAndSetMessages(displayChatId, [...currentMsgs, ...newMsgs])
        })
      } catch (err: unknown) {
        const message = err instanceof Error ? err.message : 'Bot 响应异常'
        const errMsg: Message = { id: `bot-err-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`, chatId, senderId: 'system', senderName: '系统', senderAvatar: '', content: `⚠️ ${message}`, messageType: 'text', isBot: true, createdAt: new Date().toISOString() }
        setMessages((prev) => [...prev, errMsg])
        queueMicrotask(() => {
          const currentMsgs = loadChatMessages(chatId)
          const existingIds = new Set(currentMsgs.map(m => m.id))
          const newMsgs = [localMsg, errMsg].filter(m => !existingIds.has(m.id))
          if (newMsgs.length > 0) persistAndSetMessages(chatId, [...currentMsgs, ...newMsgs])
        })
      }
    } else {
      const chatType = selectedChat.type === 'group' ? 2 : 1
      const mentionUserIds: string[] = [...pendingMentionIds]
      if (selectedChat?.type === 'group') {
        for (const bot of groupBots) {
          if (trimContent.includes(`@${bot.name}`)) {
            if (!mentionUserIds.includes(`bot_${bot.id}`)) {
              mentionUserIds.push(`bot_${bot.id}`)
            }
          }
        }
      }
      if (wsConnected) {
        const msgData: any = {
          chat_id: chatId,
          content: trimContent,
          chat_type: chatType,
          message_type: 1,
          mention_user_ids: mentionUserIds,
        }
        if (mentionUserIds.some((id: string) => id.startsWith('bot_'))) {
          const settings = getAISettings()
          if (settings.summary?.apiKey) {
            msgData.extra = {
              model_config: {
                provider: settings.summary.provider,
                model: settings.summary.model,
                api_key: settings.summary.apiKey,
                base_url: settings.summary.baseUrl,
              },
            }
          }
        }
        wsSend({
          type: 'message',
          data: msgData,
        })
      } else {
        sendMessage(chatId, trimContent).catch(() => {})
      }
      setPendingMentionIds([])
    }
  }

  const handleTyping = useCallback(() => {
    if (!selectedChat || !wsConnected) return
    wsSend({
      type: 'typing',
      data: {
        chat_id: selectedChat.id,
        typing: true,
      },
    })
  }, [selectedChat, wsConnected, wsSend])

  const handleSearchMessages = useCallback(async () => {
    if (!selectedChatId || !searchKeyword.trim()) return
    setSearchLoading(true)
    try {
      const results = await searchChatMessages(selectedChatId, searchKeyword.trim(), searchStartTime || undefined, searchEndTime || undefined)
      setSearchResults(results as Message[])
    } catch {
      setSearchResults([])
    } finally {
      setSearchLoading(false)
    }
  }, [selectedChatId, searchKeyword, searchStartTime, searchEndTime])

  const handleUpload = async (file: File) => {
    if (!selectedChat) return
    const chatId = selectedChat.id
    const mediaType = detectMediaType(file)
    let previewUrl = ''
    if (mediaType === 'image') previewUrl = URL.createObjectURL(file)
    const localId = `local-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`
    const content = mediaType === 'image' ? '' : `[${mediaType}] ${file.name}`
    const localMsg: Message = { id: localId, chatId, senderId: user?.id || '', senderName: user?.nickname || '我', senderAvatar: user?.avatar || '', content, messageType: mediaType, mediaUrl: previewUrl || undefined, mediaMeta: file.name, createdAt: new Date().toISOString() }
    
    // 保存到 sentMessagesRef，用于后续去重
    const sentKey = `${chatId}:${content.trim()}:${file.name}`
    sentMessagesRef.current.set(sentKey, { localId, timestamp: Date.now() })
    console.log('%c📤 [上传] 创建本地消息', 'background: #FF6B35; color: white; padding:2px 5px; border-radius:3px;', {
      localId, sentKey, content: `"${content}"`, mediaType, fileName: file.name, previewUrl: previewUrl ? 'blob:...' : 'none',
      sentRefSize: sentMessagesRef.current.size,
      allKeys: [...sentMessagesRef.current.keys()]
    })
    
    // 立即更新 UI 显示！
    setMessages((prev) => [...prev, localMsg])
    updateChatLastMessage(chatId, content)
    
    // 后台异步持久化
    queueMicrotask(() => {
      const currentMsgs = loadChatMessages(chatId)
      const existingIds = new Set(currentMsgs.map(m => m.id))
      if (!existingIds.has(localMsg.id)) {
        persistAndSetMessages(chatId, [...currentMsgs, localMsg])
      }
    })
    
    try {
      const uploadResult = await uploadChatMedia(chatId, file)
      const remoteUrl = uploadResult.url || ''
      const uploadMediaMeta = uploadResult.media_meta || ''
      console.log('%c📤 [上传] 上传完成', 'background: #FF6B35; color: white; padding:2px 5px; border-radius:3px;', {
        localId, remoteUrl, sentKey, uploadMediaMeta
      })
      if (remoteUrl) {
        mediaUrlToLocalIdRef.current.set(remoteUrl, localId)
        console.log('%c📤 [上传] 保存mediaUrl映射', 'background: #FF6B35; color: white; padding:2px 5px; border-radius:3px;', {
          remoteUrl, localId,
          allMappings: [...mediaUrlToLocalIdRef.current.entries()]
        })
        const metaToSend = uploadMediaMeta || JSON.stringify({ filename: file.name })
        sendMediaMessage(chatId, '', remoteUrl, mediaType, metaToSend, selectedChat.type).catch(() => {})
      }
    } catch {
      alert('上传文件失败，请重试')
    }
  }

  const handleCreateChat = async () => {
    const name = newChatName.trim()
    if (!name) return
    setNewChatError('')
    setNewChatLoading(true)

    const existing = chats.find((c) => c.type === 'private' && c.userId === name)
    if (existing) {
      setSelectedChatId(existing.id)
      setShowNewChat(false)
      setNewChatName('')
      setNewChatLoading(false)
      return
    }

    try {
      const found = await searchUsers(name)
      if (found.length === 0) {
        const directUser = await getUser(name)
        if (!directUser || !directUser.id) {
          setNewChatError('用户不存在，请检查用户名或ID')
          setNewChatLoading(false)
          return
        }
        const otherId = String(directUser.id)
        const chatId = privateChatId(String(user?.id || ''), otherId)
        const existingChat = chats.find((c) => c.id === chatId)
        if (existingChat) {
          setSelectedChatId(existingChat.id)
        } else {
          const nc: ChatItem = { id: chatId, type: 'private', name: String(directUser.nickname || directUser.username || name), avatar: String(directUser.avatar || ''), unreadCount: 0, userId: otherId }
          setChats((prev) => [nc, ...prev])
          setSelectedChatId(nc.id)
        }
      } else {
        const target = found[0]
        const otherId = String(target.id)
        const chatId = privateChatId(String(user?.id || ''), otherId)
        const existingChat = chats.find((c) => c.id === chatId)
        if (existingChat) {
          setSelectedChatId(existingChat.id)
        } else {
          const nc: ChatItem = { id: chatId, type: 'private', name: target.nickname || target.username || name, avatar: target.avatar || '', unreadCount: 0, userId: otherId }
          setChats((prev) => [nc, ...prev])
          setSelectedChatId(nc.id)
        }
      }
      setShowNewChat(false)
      setNewChatName('')
    } catch {
      setNewChatError('查找用户失败，请检查网络连接')
    } finally {
      setNewChatLoading(false)
    }
  }

  const handleSelectBot = (bot: { id: string; name: string; avatar: string }) => {
    const chatId = `bot-${bot.id}`
    const existing = chats.find((c) => c.id === chatId)
    if (existing) { setSelectedChatId(existing.id) }
    else { const nc: ChatItem = { id: chatId, type: 'bot', name: bot.name, avatar: bot.avatar || '', unreadCount: 0, botId: bot.id }; setChats((prev) => [nc, ...prev]); setSelectedChatId(nc.id) }
    setShowBotPanel(false)
  }

  const filteredChats = chats.filter((c) => !searchQuery || c.name.toLowerCase().includes(searchQuery.toLowerCase()))
  // 按最后消息时间排序，最新的在前面
  const sortedChats = [...filteredChats].sort((a, b) => {
    const timeA = a.lastMessageTime ? new Date(a.lastMessageTime).getTime() : 0
    const timeB = b.lastMessageTime ? new Date(b.lastMessageTime).getTime() : 0
    return timeB - timeA
  })

  const formatChatTime = (dateStr?: string) => {
    if (!dateStr) return ''
    const d = new Date(dateStr)
    const now = new Date()
    if (d.toDateString() === now.toDateString()) return `${d.getHours().toString().padStart(2, '0')}:${d.getMinutes().toString().padStart(2, '0')}`
    return `${(d.getMonth() + 1).toString().padStart(2, '0')}/${d.getDate().toString().padStart(2, '0')}`
  }

  return (
    <div className="chat-page">
      <div className="chat-list-panel">
        <div className="chat-list-header">
          <h2>消息</h2>
          <div className="chat-list-actions">
            <button className="chat-list-action-btn" onClick={() => { setShowAddFriend(true); setAddFriendError('') }} title="添加好友"><UserPlus size={18} /></button>
            <button className="chat-list-action-btn" onClick={async () => { const reqs = await getFriendRequests(); setFriendRequestList(reqs); setShowFriendRequests(true) }} title="好友申请"><UserCheck size={18} /></button>
            <button className="chat-list-action-btn" onClick={() => setShowCreateGroup(true)} title="创建群组">👥</button>
            <button className="chat-list-action-btn" onClick={() => { setShowJoinGroup(true); setJoinGroupError('') }} title="加入群组">🔗</button>
            <button className="chat-list-action-btn" onClick={refreshConversations} title="刷新列表"><RefreshCw size={18} /></button>
            <button className="chat-list-action-btn" onClick={() => setShowBotPanel(true)} title="Bot列表"><Bot size={18} /></button>
            <button className="chat-list-action-btn" onClick={() => setShowNewChat(true)} title="新对话"><Plus size={18} /></button>
          </div>
        </div>
        <div className="chat-list-search">
          <Search size={16} className="search-icon" />
          <input className="ba-input" placeholder="搜索对话..." value={searchQuery} onChange={(e) => setSearchQuery(e.target.value)} />
        </div>
        <div className="chat-list-items">
          {/* 有搜索词时直接显示 */}
          {searchQuery ? (
            sortedChats.map((chat) => (
              <div key={chat.id} className={`chat-list-item ${selectedChatId === chat.id ? 'active' : ''}`} onClick={() => setSelectedChatId(chat.id)}>
                {chat.type === 'group' ? (
                  <div className="ba-avatar group-avatar">
                    {(chat as any).avatar ? (
                      <img 
                        src={toMediaUrl((chat as any).avatar)} 
                        alt="" 
                        style={{ width: '100%', height: '100%', borderRadius: '50%', objectFit: 'cover' }} 
                      />
                    ) : (
                      <Users size={20} />
                    )}
                  </div>
                ) : (
                  <div className={`ba-avatar ${chat.type === 'bot' ? 'bot-avatar' : ''}`} style={{ position: 'relative' }}>
                    {chat.type === 'bot' ? (
                      chat.avatar ? (
                        chat.avatar.startsWith('http') || chat.avatar.startsWith('/') ? (
                          <img src={toMediaUrl(chat.avatar)} alt="" style={{ width: '100%', height: '100%', borderRadius: '50%', objectFit: 'cover' }} />
                        ) : (
                          <span style={{ fontSize: 18 }}>{chat.avatar}</span>
                        )
                      ) : (
                        <Bot size={18} />
                      )
                    ) : (
                      (chat as any).avatar ? (
                        <img 
                          src={toMediaUrl((chat as any).avatar)} 
                          alt="" 
                          style={{ width: '100%', height: '100%', borderRadius: '50%', objectFit: 'cover' }} 
                        />
                      ) : (
                        (chat.name && chat.name.length > 0 ? chat.name.charAt(0) : '用')
                      )
                    )}
                    {chat.type === 'private' && chat.userId && (
                      <span className={`online-dot ${onlineStatusMap.get(chat.userId) ? 'online' : 'offline'}`} />
                    )}
                  </div>
                )}
                <div className="chat-list-item-info">
                  <div className="chat-list-item-top">
                    <span className="chat-list-item-name">{chat.name}</span>
                    <span className="chat-list-item-time">{formatChatTime(chat.lastMessageTime)}</span>
                  </div>
                  <div className="chat-list-item-bottom">
                    <span className="chat-list-item-msg">{chat.lastMessage || '暂无消息'}</span>
                    {chat.unreadCount > 0 && <span className="chat-list-item-badge">{chat.unreadCount}</span>}
                  </div>
                </div>
              </div>
            ))
          ) : (
            <>
              {/* 群组分组 */}
              {(() => {
                const groupChats = sortedChats.filter(c => c.type === 'group')
                const isCollapsed = collapsedGroups.has('group')
                return (
                  <div className="chat-group-section">
                    <div 
                      className="chat-group-header" 
                      onClick={(e) => { e.stopPropagation(); toggleGroupCollapse('group'); }}
                    >
                      <span className="chat-group-arrow">
                        {isCollapsed ? '▶' : '▼'}
                      </span>
                      <span className="chat-group-name">群组</span>
                      <span className="chat-group-count">({groupChats.length})</span>
                    </div>
                    {!isCollapsed && (
                      <div className="chat-group-content">
                        {groupChats.map((chat) => (
                          <div key={chat.id} className={`chat-list-item ${selectedChatId === chat.id ? 'active' : ''}`} onClick={() => setSelectedChatId(chat.id)}>
                            <div className="ba-avatar group-avatar">
                              {(chat as any).avatar ? (
                                <img 
                                  src={toMediaUrl((chat as any).avatar)} 
                                  alt="" 
                                  style={{ width: '100%', height: '100%', borderRadius: '50%', objectFit: 'cover' }} 
                                />
                              ) : (
                                <Users size={20} />
                              )}
                            </div>
                            <div className="chat-list-item-info">
                              <div className="chat-list-item-top">
                                <span className="chat-list-item-name">{chat.name}</span>
                                <span className="chat-list-item-time">{formatChatTime(chat.lastMessageTime)}</span>
                              </div>
                              <div className="chat-list-item-bottom">
                                <span className="chat-list-item-msg">{chat.lastMessage || '暂无消息'}</span>
                                {chat.unreadCount > 0 && <span className="chat-list-item-badge">{chat.unreadCount}</span>}
                              </div>
                            </div>
                          </div>
                        ))}
                      </div>
                    )}
                  </div>
                )
              })()}

              {/* 好友分组 */}
              {(() => {
                console.log(`%c🟠 [前端] 分组筛选前的所有sortedChats`, 'background: #FF8800; color: black; font-size:11px; padding:2px 4px; border-radius:3px;', 
                  sortedChats.map(c => ({ id: c.id, type: c.type, isFriend: c.isFriend, name: c.name })))
                
                const friendChats = sortedChats.filter(c => c.type === 'private' && c.isFriend === true)
                
                console.log(`%c🟢 [前端] 好友分组包含的聊天`, 'background: #00AA00; color: white; font-size:11px; padding:2px 4px; border-radius:3px;', 
                  friendChats.map(c => ({ id: c.id, isFriend: c.isFriend, name: c.name })))
                
                const isCollapsed = collapsedGroups.has('private')
                return (
                  <div className="chat-group-section">
                    <div 
                      className="chat-group-header" 
                      onClick={(e) => { e.stopPropagation(); toggleGroupCollapse('private'); }}
                    >
                      <span className="chat-group-arrow">
                        {isCollapsed ? '▶' : '▼'}
                      </span>
                      <span className="chat-group-name">好友</span>
                      <span className="chat-group-count">({friendChats.length})</span>
                    </div>
                    {!isCollapsed && (
                      <div className="chat-group-content">
                        {friendChats.map((chat) => (
                          <div key={chat.id} className={`chat-list-item ${selectedChatId === chat.id ? 'active' : ''}`} onClick={() => setSelectedChatId(chat.id)}>
                            <div className="ba-avatar" style={{ position: 'relative' }}>
                              {(chat as any).avatar ? (
                                <img 
                                  src={toMediaUrl((chat as any).avatar)} 
                                  alt="" 
                                  style={{ width: '100%', height: '100%', borderRadius: '50%', objectFit: 'cover' }} 
                                />
                              ) : (
                                (chat.name && chat.name.length > 0 ? chat.name.charAt(0) : '用')
                              )}
                              {chat.userId && (
                                <span className={`online-dot ${onlineStatusMap.get(chat.userId) ? 'online' : 'offline'}`} />
                              )}
                            </div>
                            <div className="chat-list-item-info">
                              <div className="chat-list-item-top">
                                <span className="chat-list-item-name">{chat.name}</span>
                                <span className="chat-list-item-time">{formatChatTime(chat.lastMessageTime)}</span>
                              </div>
                              <div className="chat-list-item-bottom">
                                <span className="chat-list-item-msg">{chat.lastMessage || '暂无消息'}</span>
                                {chat.unreadCount > 0 && <span className="chat-list-item-badge">{chat.unreadCount}</span>}
                              </div>
                            </div>
                          </div>
                        ))}
                      </div>
                    )}
                  </div>
                )
              })()}

              {/* 陌生人分组 */}
              {(() => {
                const strangerChats = sortedChats.filter(c => c.type === 'private' && c.isFriend !== true)
                
                console.log(`%c🔴 [前端] 陌生人分组包含的聊天`, 'background: #FF4444; color: white; font-size:11px; padding:2px 4px; border-radius:3px;', 
                  strangerChats.map(c => ({ id: c.id, isFriend: c.isFriend, name: c.name })))
                
                if (strangerChats.length === 0) return null
                const isCollapsed = collapsedGroups.has('stranger')
                return (
                  <div className="chat-group-section">
                    <div 
                      className="chat-group-header" 
                      onClick={(e) => { e.stopPropagation(); toggleGroupCollapse('stranger'); }}
                    >
                      <span className="chat-group-arrow">
                        {isCollapsed ? '▶' : '▼'}
                      </span>
                      <span className="chat-group-name">陌生人</span>
                      <span className="chat-group-count">({strangerChats.length})</span>
                    </div>
                    {!isCollapsed && (
                      <div className="chat-group-content">
                        {strangerChats.map((chat) => (
                          <div key={chat.id} className={`chat-list-item ${selectedChatId === chat.id ? 'active' : ''}`} onClick={() => setSelectedChatId(chat.id)}>
                            <div className="ba-avatar" style={{ opacity: 0.7, position: 'relative' }}>
                              {(chat as any).avatar ? (
                                <img 
                                  src={toMediaUrl((chat as any).avatar)} 
                                  alt="" 
                                  style={{ width: '100%', height: '100%', borderRadius: '50%', objectFit: 'cover' }} 
                                />
                              ) : (
                                (chat.name && chat.name.length > 0 ? chat.name.charAt(0) : '?')
                              )}
                              {chat.userId && (
                                <span className={`online-dot ${onlineStatusMap.get(chat.userId) ? 'online' : 'offline'}`} />
                              )}
                            </div>
                            <div className="chat-list-item-info">
                              <div className="chat-list-item-top">
                                <span className="chat-list-item-name">{chat.name} <span style={{ fontSize: 11, color: 'var(--ba-text-light)', fontWeight: 400 }}>[陌生人]</span></span>
                                <span className="chat-list-item-time">{formatChatTime(chat.lastMessageTime)}</span>
                              </div>
                              <div className="chat-list-item-bottom">
                                <span className="chat-list-item-msg">{chat.lastMessage || '暂无消息'}</span>
                                {chat.unreadCount > 0 && <span className="chat-list-item-badge">{chat.unreadCount}</span>}
                              </div>
                            </div>
                          </div>
                        ))}
                      </div>
                    )}
                  </div>
                )
              })()}

              {/* Bot分组 */}
              {(() => {
                const botChats = sortedChats.filter(c => c.type === 'bot')
                const isCollapsed = collapsedGroups.has('bot')
                return (
                  <div className="chat-group-section">
                    <div 
                      className="chat-group-header" 
                      onClick={(e) => { e.stopPropagation(); toggleGroupCollapse('bot'); }}
                    >
                      <span className="chat-group-arrow">
                        {isCollapsed ? '▶' : '▼'}
                      </span>
                      <span className="chat-group-name">Bot</span>
                      <span className="chat-group-count">({botChats.length})</span>
                    </div>
                    {!isCollapsed && (
                      <div className="chat-group-content">
                        {botChats.map((chat) => (
                          <div key={chat.id} className={`chat-list-item ${selectedChatId === chat.id ? 'active' : ''}`} onClick={() => setSelectedChatId(chat.id)}>
                            <div className="ba-avatar bot-avatar">
                              {chat.avatar ? (
                                chat.avatar.startsWith('http') || chat.avatar.startsWith('/') ? (
                                  <img src={toMediaUrl(chat.avatar)} alt="" style={{ width: '100%', height: '100%', borderRadius: '50%', objectFit: 'cover' }} />
                                ) : (
                                  <span style={{ fontSize: 18 }}>{chat.avatar}</span>
                                )
                              ) : (
                                <Bot size={18} />
                              )}
                            </div>
                            <div className="chat-list-item-info">
                              <div className="chat-list-item-top">
                                <span className="chat-list-item-name">{chat.name}</span>
                                <span className="chat-list-item-time">{formatChatTime(chat.lastMessageTime)}</span>
                              </div>
                              <div className="chat-list-item-bottom">
                                <span className="chat-list-item-msg">{chat.lastMessage || '暂无消息'}</span>
                                {chat.unreadCount > 0 && <span className="chat-list-item-badge">{chat.unreadCount}</span>}
                              </div>
                            </div>
                          </div>
                        ))}
                      </div>
                    )}
                  </div>
                )
              })()}
            </>
          )}

          {filteredChats.length === 0 && <div className="chat-list-empty">暂无对话</div>}
        </div>
      </div>

      <div className="chat-window">
        {selectedChat ? (
          <>
            <div className="chat-window-header">
              <div className="chat-window-header-left">
                <div 
                  className={`ba-avatar ba-avatar-sm ${selectedChat.type === 'bot' ? 'bot-avatar' : selectedChat.type === 'group' ? 'group-avatar' : ''}`}
                  style={{ position: 'relative', cursor: selectedChat.type === 'group' ? 'pointer' : 'default' }}
                  onClick={() => selectedChat.type === 'group' && avatarRef.current?.click()}
                >
                  {selectedChat.type === 'bot' ? (
                    selectedChat.avatar ? (
                      selectedChat.avatar.startsWith('http') || selectedChat.avatar.startsWith('/') ? (
                        <img src={toMediaUrl(selectedChat.avatar)} alt="" style={{ width: '100%', height: '100%', borderRadius: '50%', objectFit: 'cover' }} />
                      ) : (
                        <span style={{ fontSize: 16 }}>{selectedChat.avatar}</span>
                      )
                    ) : (
                      <Bot size={16} />
                    )
                  ) : (
                    (selectedChat as any).avatar ? (
                      <img 
                        src={toMediaUrl((selectedChat as any).avatar)} 
                        alt="" 
                        style={{ width: '100%', height: '100%', borderRadius: '50%', objectFit: 'cover' }} 
                      />
                    ) : (
                      selectedChat.type === 'group' ? (
                        <Users size={16} />
                      ) : (
                        selectedChat.name.charAt(0)
                      )
                    )
                  )}
                  {selectedChat.type === 'group' && (
                    <div style={{ 
                      position: 'absolute', 
                      bottom: -4, 
                      right: -4, 
                      background: 'var(--ba-blue)', 
                      borderRadius: '50%', 
                      width: 20, 
                      height: 20, 
                      display: 'flex', 
                      alignItems: 'center', 
                      justifyContent: 'center',
                      border: '2px solid var(--ba-bg-primary)',
                      boxShadow: '0 2px 8px rgba(0,0,0,0.2)'
                    }}>
                      <Camera size={10} color="white" />
                    </div>
                  )}
                  {selectedChat.type === 'private' && selectedChat.userId && (
                    <span className={`online-dot ${onlineStatusMap.get(selectedChat.userId) ? 'online' : 'offline'}`} />
                  )}
                </div>
                <div>
                  <div className="chat-window-title">{selectedChat.name}</div>
                  <div className="chat-window-status">
                    {selectedChat.type === 'bot' ? (
                      <span style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                        <Bot size={12} /> AI Bot
                        <label style={{ display: 'flex', alignItems: 'center', gap: 4, cursor: 'pointer', fontSize: 11, color: 'var(--ba-text-light)' }}>
                          <input type="checkbox" checked={autoSaveToKb} onChange={(e) => setAutoSaveToKb(e.target.checked)} style={{ width: 12, height: 12 }} />
                          自动存入知识库
                        </label>
                      </span>
                    ) : selectedChat.type === 'group' ? `群组聊天 · ${groupMembers.size} 成员` : (
                      <>
                        <span className={`online-dot ${onlineStatusMap.get(selectedChat.userId || '') ? 'online' : 'offline'}`} style={{ position: 'static', display: 'inline-block', width: 8, height: 8, border: 'none' }} />
                        {onlineStatusMap.get(selectedChat.userId || '') ? '在线' : '离线'}
                      </>
                    )}
                  </div>
                </div>
              </div>
              <input ref={avatarRef} type="file" accept="image/*" hidden onChange={() => { const f = avatarRef.current?.files?.[0]; if (f) handleGroupAvatarUpload(f) }} />
              <div className="chat-window-header-actions" ref={menuRef} style={{ position: 'relative' }}>
                <button className="header-action-btn" title="搜索消息" onClick={() => { setShowSearchPanel(!showSearchPanel); setSearchResults([]) }}><Search size={18} /></button>
                {selectedChat.type === 'group' && (
                  <button className="header-action-btn" title="邀请成员" onClick={() => { setShowAddMember(true); setAddMemberError('') }}><Plus size={18} /></button>
                )}
                {selectedChat.type !== 'group' && (
                  <>
                    <button className="header-action-btn" title="语音通话"><Phone size={18} /></button>
                    <button className="header-action-btn" title="视频通话"><Video size={18} /></button>
                  </>
                )}
                <button className="header-action-btn" title="更多" onClick={() => setShowMenu(!showMenu)}><MoreVertical size={18} /></button>
                {showMenu && (
                  <div className="header-dropdown-menu">
                    <button className="header-dropdown-item" onClick={handleSummarize}>
                      <Sparkles size={16} /> 生成摘要
                    </button>
                    <button className="header-dropdown-item" onClick={() => { handleGenerateReplies(); setShowMenu(false) }}>
                      <MessageSquare size={16} /> 智能回复
                    </button>
                    <button className="header-dropdown-item" onClick={() => { setShowMenu(false) }}>
                      <FileText size={16} /> 聊天记录
                    </button>
                    {selectedChat.type === 'group' && isOwner && (
                      <button className="header-dropdown-item" onClick={() => { setShowTransferOwner(true); setShowMenu(false) }}>
                        👑 转让群主
                      </button>
                    )}
                    {selectedChat.type === 'group' && (isOwner || isAdmin) && (
                      <button className="header-dropdown-item" onClick={() => { setAnnouncementText(groupInfo?.announcement || ''); setShowAnnouncement(true); setShowMenu(false) }}>
                        📢 编辑公告
                      </button>
                    )}
                    {selectedChat.type === 'group' && (
                      <button 
                        className="header-dropdown-item" 
                        onClick={handleDeleteChatHistory}
                        style={{ color: '#faad14' }}
                      >
                        🧹 清理聊天记录
                      </button>
                    )}
                    {selectedChat.type === 'private' && selectedChat.isFriend && (
                      <button 
                        className="header-dropdown-item" 
                        onClick={handleDeleteFriend}
                        style={{ color: '#ff4d4f' }}
                      >
                        <UserX size={16} /> 删除好友
                      </button>
                    )}
                    {selectedChat.type === 'private' && !selectedChat.isFriend && selectedChat.userId && (
                      <button 
                        className="header-dropdown-item" 
                        onClick={handleAddFriend}
                        style={{ color: '#1890ff' }}
                      >
                        <UserPlus size={16} /> 添加好友
                      </button>
                    )}
                    <button 
                      className="header-dropdown-item" 
                      onClick={handleDeleteChat}
                      style={{ color: '#ff4d4f' }}
                    >
                      {selectedChat.type === 'group' ? '🚪 退出群组' : '🗑️ 删除聊天'}
                    </button>
                  </div>
                )}
              </div>
            </div>

            {/* 群组标签页 */}
            {selectedChat.type === 'group' && (
              <div className="group-tabs">
                <button 
                  className={`group-tab ${rightTab === 'chat' ? 'active' : ''}`} 
                  onClick={() => {
                    setRightTab('chat')
                    // 强制滚动到底部！
                    const doScroll = () => {
                      setTimeout(() => {
                        messagesEndRef.current?.scrollIntoView({ behavior: 'auto' })
                      }, 50)
                    }
                    doScroll()
                    setTimeout(() => doScroll(), 150)
                    setTimeout(() => doScroll(), 300)
                  }}
                >
                  💬 聊天
                </button>
                <button 
                  className={`group-tab ${rightTab === 'members' ? 'active' : ''}`} 
                  onClick={() => setRightTab('members')}
                >
                  👥 成员 ({groupMembers.size})
                </button>
                <button
                  className={`group-tab ${rightTab === 'announcement' ? 'active' : ''}`}
                  onClick={() => setRightTab('announcement')}
                >
                  📢 公告
                </button>
                <button
                  className={`group-tab ${rightTab === 'assistant' ? 'active' : ''}`}
                  onClick={() => setRightTab('assistant')}
                >
                  🤖 助手
                </button>
              </div>
            )}

            {selectedChat.type === 'group' && rightTab === 'announcement' ? (
              <div className="group-announcement-panel">
                <div className="group-announcement-content">
                  {groupInfo?.announcement ? (
                    <div className="group-announcement-text">{groupInfo.announcement}</div>
                  ) : (
                    <div className="group-announcement-empty">暂无公告</div>
                  )}
                </div>
                {(isOwner || isAdmin) && (
                  <button
                    className="ba-btn ba-btn-primary"
                    onClick={() => { setAnnouncementText(groupInfo?.announcement || ''); setShowAnnouncement(true) }}
                    style={{ marginTop: 16 }}
                  >
                    编辑公告
                  </button>
                )}
              </div>
            ) : selectedChat.type === 'group' && rightTab === 'members' ? (
              <div className="group-members">
                {(isOwner || isAdmin) && (
                  <div style={{ padding: '8px 0', borderBottom: '1px solid var(--ba-border)', marginBottom: 8 }}>
                    <button className="ba-btn ba-btn-primary" style={{ width: '100%', fontSize: 13 }} onClick={() => setShowInviteBot(true)}>
                      <Bot size={14} style={{ marginRight: 4 }} /> 邀请Bot
                    </button>
                  </div>
                )}
                {groupMembers.size === 0 ? (
                  <div className="chat-list-empty" style={{ padding: '24px 0', color: 'var(--ba-text-light)' }}>
                    暂无成员
                  </div>
                ) : (
                  <div className="group-members-grid">
                    {Array.from(groupMembers.values()).map((member: any) => {
                      const isBotMember = member.userId.startsWith('bot_')
                      return (
                        <div key={member.userId} className="group-member-card ba-card">
                          <div className="ba-avatar ba-avatar-sm">
                            {member.avatar ? (
                              <img src={toMediaUrl(member.avatar)} alt="" style={{ width: '100%', height: '100%', borderRadius: '50%', objectFit: 'cover' }} />
                            ) : isBotMember ? (
                              <Bot size={16} />
                            ) : (
                              (member.username || member.userId).charAt(0) || '?'
                            )}
                          </div>
                          <div className="group-member-info">
                            <div className="group-member-name">
                              {isBotMember ? (bots.find(b => b.id === member.userId.replace('bot_', ''))?.name || member.username || `Bot`) : (member.username || `用户 ${member.userId.slice(0, 8)}`)}
                              {isBotMember && <span className="bot-badge">Bot</span>}
                              {roleIcon(member.role)}
                            </div>
                            <div className="group-member-role">
                              {isBotMember ? '机器人' : member.role === 'owner' ? '群主' : member.role === 'admin' ? '管理员' : '成员'}
                            </div>
                          </div>
                          {isOwner && !isBotMember && member.userId !== user?.id && (
                            <div style={{ display: 'flex', gap: '4px' }}>
                              <button
                                className="group-member-remove"
                                onClick={() => { setTransferToUserId(member.userId); setShowTransferOwner(true) }}
                                title="转让群主"
                              >
                                <Crown size={14} />
                              </button>
                              {member.role === 'admin' ? (
                                <button
                                  className="group-member-remove"
                                  onClick={() => { setSetAdminMemberId(member.userId); setSetAdminIsAdmin(false); setShowSetAdmin(true) }}
                                  title="取消管理员"
                                >
                                  <ShieldOff size={14} />
                                </button>
                              ) : (
                                <button
                                  className="group-member-remove"
                                  onClick={() => { setSetAdminMemberId(member.userId); setSetAdminIsAdmin(true); setShowSetAdmin(true) }}
                                  title="设为管理员"
                                >
                                  <Shield size={14} />
                                </button>
                              )}
                              <button
                                className="group-member-remove"
                                onClick={() => { setMuteMemberId(member.userId); setShowMuteMember(true) }}
                                title="禁言"
                              >
                                <VolumeX size={14} />
                              </button>
                              <button
                                className="group-member-remove"
                                onClick={() => handleRemoveMember(member.userId)}
                                title="移除成员"
                              >
                                <UserMinus size={14} />
                              </button>
                            </div>
                          )}
                          {isOwner && isBotMember && (
                            <button
                              className="group-member-remove"
                              onClick={() => handleRemoveMember(member.userId)}
                              title="移除Bot"
                            >
                              <UserMinus size={14} />
                            </button>
                          )}
                          {!isOwner && isAdmin && !isBotMember && member.role === 'member' && member.userId !== user?.id && (
                            <div style={{ display: 'flex', gap: '4px' }}>
                              <button
                                className="group-member-remove"
                                onClick={() => { setMuteMemberId(member.userId); setShowMuteMember(true) }}
                                title="禁言"
                              >
                                <VolumeX size={14} />
                              </button>
                              <button
                                className="group-member-remove"
                                onClick={() => handleRemoveMember(member.userId)}
                                title="移除成员"
                              >
                                <UserMinus size={14} />
                              </button>
                            </div>
                          )}
                          {!isOwner && !isAdmin && member.userId === user?.id && (
                            <div style={{ color: 'var(--ba-text-light)', fontSize: '12px' }}>你</div>
                          )}
                        </div>
                      )
                    })}
                  </div>
                )}
              </div>
            ) : selectedChat.type === 'group' && rightTab === 'assistant' ? (
              <div className="group-assistant-panel">
                <div className="group-assistant-cards">
                  <div className="group-assistant-card ba-card" onClick={() => { setSummaryMessageCount(50); setShowSummary(true); setSummary(''); setSummaryKeyPoints([]); setSummaryTodos([]); setSummaryCandidates([]); setSummaryParticipants([]) }}>
                    <div className="group-assistant-card-icon">📝</div>
                    <div className="group-assistant-card-title">对话摘要</div>
                    <div className="group-assistant-card-desc">总结群聊要点与关键决策</div>
                  </div>
                  <div className="group-assistant-card ba-card" onClick={async () => {
                    if (!selectedChatId) return
                    setReplyLoading(true)
                    setShowReplyCandidates(true)
                    setReplyCandidates([])
                    try {
                      const res = await client.post('/summary/reply-candidates', { chat_id: selectedChatId, chat_type: 2, candidate_count: 3, tone: 'friendly', message_count: summaryMessageCount, model_config: getAISettings().reply })
                      const d = res.data as Record<string, unknown>
                      setReplyCandidates(Array.isArray(d.data) ? d.data : [])
                    } catch { setReplyCandidates([]) }
                    finally { setReplyLoading(false) }
                  }}>
                    <div className="group-assistant-card-icon">💬</div>
                    <div className="group-assistant-card-title">智能回复</div>
                    <div className="group-assistant-card-desc">根据上下文生成回复建议</div>
                  </div>
                  <div className="group-assistant-card ba-card" onClick={async () => {
                    if (!selectedChatId) return
                    setSummaryLoading(true)
                    setShowSummary(true)
                    setSummary('')
                    setSummaryKeyPoints([])
                    setSummaryTodos([])
                    setSummaryCandidates([])
                    setSummaryParticipants([])
                    try {
                      const res = await client.post('/summary/extract-todos', { chat_id: selectedChatId, chat_type: 2, message_count: summaryMessageCount, model_config: getAISettings().todo })
                      const d = res.data as Record<string, unknown>
                      const inner = (d.data || d) as Record<string, unknown>
                      setSummary('待办事项提取结果')
                      setSummaryTodos(Array.isArray(inner) ? inner : Array.isArray(d.data) ? d.data as any[] : [])
                    } catch { setSummary('提取待办事项失败') }
                    finally { setSummaryLoading(false) }
                  }}>
                    <div className="group-assistant-card-icon">✅</div>
                    <div className="group-assistant-card-title">待办提取</div>
                    <div className="group-assistant-card-desc">从对话中提取待办事项</div>
                  </div>
                  <div className="group-assistant-card ba-card" onClick={async () => {
                    if (!selectedChatId) return
                    try {
                      const res = await client.get('/qa/recommended', { params: { chat_id: selectedChatId, limit: 5 } })
                      const d = res.data as Record<string, unknown>
                      const questions = Array.isArray(d.data) ? d.data as { question: string }[] : []
                      if (questions.length > 0) {
                        setSummary('推荐问题')
                        setSummaryKeyPoints(questions.map(item => item.question))
                        setSummaryPanelType('questions')
                        setShowSummary(true)
                        setSummaryTodos([])
                        setSummaryCandidates([])
                        setSummaryParticipants([])
                      } else {
                        setSummary('暂无推荐问题')
                        setSummaryPanelType('questions')
                        setShowSummary(true)
                      }
                    } catch {
                      setSummary('获取推荐问题失败')
                      setShowSummary(true)
                    }
                  }}>
                    <div className="group-assistant-card-icon">❓</div>
                    <div className="group-assistant-card-title">推荐问题</div>
                    <div className="group-assistant-card-desc">基于对话内容推荐相关问题</div>
                  </div>
                  <div className="group-assistant-card ba-card" onClick={async () => {
                    if (!selectedChatId) return
                    try {
                      const res = await client.get('/recommend/items', { params: { type: 'chat', limit: 5 } })
                      const d = res.data as Record<string, unknown>
                      const items = Array.isArray(d.data) ? d.data as { title: string; description?: string }[] : []
                      if (items.length > 0) {
                        setSummary('智能推荐')
                        setSummaryKeyPoints(items.map(item => item.title))
                        setSummaryPanelType('recommend')
                        setSummaryCandidates(items as any[])
                        setShowSummary(true)
                        setSummaryTodos([])
                        setSummaryParticipants([])
                      } else {
                        setSummary('暂无推荐内容')
                        setSummaryPanelType('recommend')
                        setShowSummary(true)
                      }
                    } catch {
                      setSummary('获取推荐内容失败')
                      setShowSummary(true)
                    }
                  }}>
                    <div className="group-assistant-card-icon">💡</div>
                    <div className="group-assistant-card-title">智能推荐</div>
                    <div className="group-assistant-card-desc">推荐相关内容与资源</div>
                  </div>
                </div>
              </div>
            ) : (
              <>
                {showSearchPanel && (
                  <div className="chat-search-panel">
                    <div className="chat-search-panel-row">
                      <input type="text" placeholder="搜索关键词..." value={searchKeyword} onChange={(e) => setSearchKeyword(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && handleSearchMessages()} />
                      <input type="date" value={searchStartTime} onChange={(e) => setSearchStartTime(e.target.value)} title="开始时间" />
                      <input type="date" value={searchEndTime} onChange={(e) => setSearchEndTime(e.target.value)} title="结束时间" />
                      <button className="chat-search-panel-btn primary" onClick={handleSearchMessages} disabled={searchLoading}>{searchLoading ? '搜索中...' : '搜索'}</button>
                      <button className="chat-search-panel-btn secondary" onClick={() => { setShowSearchPanel(false); setSearchResults([]); setSearchKeyword(''); setSearchStartTime(''); setSearchEndTime('') }}>关闭</button>
                    </div>
                    {searchResults.length > 0 && (
                      <div className="chat-search-results">
                        {searchResults.map((msg) => (
                          <div key={msg.id} className="chat-search-result-item">
                            <div className="result-sender">{msg.senderName}</div>
                            <div className="result-content">{msg.content}</div>
                            <div className="result-time">{msg.createdAt}</div>
                          </div>
                        ))}
                      </div>
                    )}
                  </div>
                )}
                <div className="chat-window-messages">
                  {messages.map((msg, i) => (
                    <ChatBubble
                      key={msg.id || i}
                      message={msg}
                      isOwn={msg.senderId === user?.id}
                      showAvatar={true}
                      onEdit={handleEditMessage}
                      onForward={handleForwardMessage}
                      onWithdraw={handleWithdrawMessage}
                    />
                  ))}
                  {typingChatId === selectedChatId && selectedChat?.type !== 'group' && (
                    <div className="chat-typing-indicator">
                      <div className="chat-typing-indicator-dots">
                        <span /><span /><span />
                      </div>
                      对方正在输入...
                    </div>
                  )}
                  <div ref={messagesEndRef} />
                </div>

                <div className="chat-reply-fab" onClick={handleGenerateReplies} title="智能回复">
                  <MessageSquare size={18} />
                </div>

                {showReplyCandidates && replyCandidates.length > 0 && (
                  <div className="chat-reply-panel">
                    <div className="chat-reply-panel-header">
                      智能回复
                      <button className="chat-reply-panel-close" onClick={() => setShowReplyCandidates(false)}>✕</button>
                    </div>
                    <div className="chat-reply-panel-body">
                      {replyCandidates.map((c: any, i: number) => (
                        <div key={i} className="chat-reply-item" onClick={() => handleSelectReply(c.content)}>
                          <div className="chat-reply-item-text">{c.content}</div>
                          <div className="chat-reply-item-conf">{Math.round((c.confidence || 0) * 100)}%</div>
                        </div>
                      ))}
                    </div>
                  </div>
                )}

                {showReplyCandidates && replyLoading && (
                  <div className="chat-reply-panel">
                    <div className="chat-reply-panel-header">
                      正在生成回复建议...
                      <button className="chat-reply-panel-close" onClick={() => { setShowReplyCandidates(false); setReplyLoading(false) }}>✕</button>
                    </div>
                    <div className="chat-reply-panel-body" style={{ textAlign: 'center', color: 'var(--ba-text-light)', padding: '20px 0' }}>
                      <RefreshCw size={20} className="spin" />
                    </div>
                  </div>
                )}

                {selectedChat.type === 'group' && groupBots.length > 0 && (
                  <div className="group-bot-bar">
                    {groupBots.map((bot) => (
                      <button
                        key={bot.id}
                        className="group-bot-btn"
                        onClick={() => {
                          const input = document.querySelector('.message-input textarea') as HTMLTextAreaElement
                          if (input) {
                            const start = input.selectionStart
                            const end = input.selectionEnd
                            const text = input.value
                            const newText = text.substring(0, start) + `@${bot.name} ` + text.substring(end)
                            input.value = newText
                            input.setSelectionRange(start + bot.name.length + 2, start + bot.name.length + 2)
                            input.dispatchEvent(new Event('input', { bubbles: true }))
                          }
                        }}
                      >
                        🤖 {bot.name}
                      </button>
                    ))}
                  </div>
                )}

                <MessageInput
                  onSend={handleSend}
                  onUpload={handleUpload}
                  onAtBot={selectedChat.type !== 'bot' ? () => setAtBotMode(true) : undefined}
                  onTyping={handleTyping}
                  placeholder={atBotMode ? '@Bot 模式 - 输入问题...' : selectedChat.type === 'bot' ? '向 Bot 提问...' : '输入消息...'}
                  bots={selectedChat.type === 'group' ? groupBots : undefined}
                  onMentionBot={selectedChat.type === 'group' ? handleMentionBot : undefined}
                />
              </>
            )}
          </>
        ) : (
          <div className="chat-window-empty">
            <div className="empty-icon">💬</div>
            <div className="empty-text">选择一个对话开始聊天</div>
          </div>
        )}
      </div>

      <Modal open={showSummary} onClose={() => setShowSummary(false)} title="对话摘要" style={{ maxWidth: 600 }}>
        <div style={{ minHeight: 100, maxHeight: '70vh', overflowY: 'auto' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 16, padding: '8px 12px', background: 'var(--ba-bg)', borderRadius: 8 }}>
            <span style={{ fontSize: 13, color: '#0ff' }}>分析最近</span>
            <select value={summaryMessageCount} onChange={(e) => setSummaryMessageCount(Number(e.target.value))} style={{ padding: '4px 8px', borderRadius: 6, border: '1px solid #0ff', background: 'var(--ba-card)', fontSize: 13, color: '#0ff', fontWeight: 600 }}>
              <option value={20}>20条</option>
              <option value={50}>50条</option>
              <option value={100}>100条</option>
              <option value={200}>200条</option>
              <option value={500}>500条</option>
            </select>
            <span style={{ fontSize: 13, color: '#0ff' }}>消息</span>
            <button className="ba-btn ba-btn-primary" style={{ marginLeft: 'auto', padding: '4px 16px', fontSize: 13 }} onClick={handleSummarize} disabled={summaryLoading}>生成摘要</button>
          </div>
          {summaryLoading ? (
            <div style={{ textAlign: 'center', padding: '24px 0' }}>
              <RefreshCw size={24} className="spin" style={{ margin: '0 auto 12px' }} />
              <p style={{ color: 'var(--ba-text-light)' }}>正在分析对话内容...</p>
            </div>
          ) : (
            <>
              {summary && (
                <div style={{ marginBottom: 20 }}>
                  <h4 style={{ fontSize: 14, fontWeight: 700, marginBottom: 8, color: summaryPanelType === 'questions' ? 'var(--ba-purple)' : summaryPanelType === 'recommend' ? 'var(--ba-orange)' : 'var(--ba-blue)' }}>
                    {summaryPanelType === 'questions' ? '❓ 推荐问题' : summaryPanelType === 'recommend' ? '💡 智能推荐' : '📝 摘要'}
                  </h4>
                  <p style={{ lineHeight: 1.8, whiteSpace: 'pre-wrap', fontSize: 14 }}>{summary}</p>
                </div>
              )}
              {summaryKeyPoints.length > 0 && (
                <div style={{ marginBottom: 20 }}>
                  <h4 style={{ fontSize: 14, fontWeight: 700, marginBottom: 8, color: 'var(--ba-cyan)' }}>{summaryPanelType === 'questions' ? '📋 问题列表' : summaryPanelType === 'recommend' ? '📋 推荐内容' : '🔑 要点'}</h4>
                  <ul style={{ paddingLeft: 20, margin: 0 }}>
                    {summaryKeyPoints.map((kp, i) => (
                      <li key={i} style={{ fontSize: 14, lineHeight: 1.8 }}>{kp}</li>
                    ))}
                  </ul>
                </div>
              )}
              {summaryParticipants.length > 0 && (
                <div style={{ marginBottom: 20 }}>
                  <h4 style={{ fontSize: 14, fontWeight: 700, marginBottom: 8, color: 'var(--ba-success)' }}>👥 参与者</h4>
                  <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
                    {summaryParticipants.map((p, i) => (
                      <span key={i} style={{ background: 'var(--ba-bg)', padding: '4px 12px', borderRadius: 12, fontSize: 13 }}>{p}</span>
                    ))}
                  </div>
                </div>
              )}
              {summaryTodos.length > 0 && (
                <div style={{ marginBottom: 20 }}>
                  <h4 style={{ fontSize: 14, fontWeight: 700, marginBottom: 8, color: 'var(--ba-warning)' }}>✅ 待办事项</h4>
                  {summaryTodos.map((todo: any, i: number) => (
                    <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '8px 12px', background: 'var(--ba-bg)', borderRadius: 8, marginBottom: 6 }}>
                      <input type="checkbox" checked={todo.status === 'completed'} readOnly style={{ accentColor: 'var(--ba-blue)' }} />
                      <div style={{ flex: 1 }}>
                        <div style={{ fontSize: 14, fontWeight: 500 }}>{todo.content}</div>
                        <div style={{ fontSize: 12, color: 'var(--ba-text-light)' }}>
                          {todo.assignee && `负责人: ${todo.assignee}`}
                          {todo.deadline && ` · 截止: ${todo.deadline}`}
                        </div>
                      </div>
                      <span style={{ fontSize: 11, padding: '2px 8px', borderRadius: 8, background: todo.status === 'completed' ? 'var(--ba-success)' : todo.status === 'in_progress' ? 'var(--ba-warning)' : 'var(--ba-bg)', color: todo.status === 'pending' ? 'var(--ba-text-light)' : '#fff' }}>
                        {todo.status === 'completed' ? '已完成' : todo.status === 'in_progress' ? '进行中' : '待处理'}
                      </span>
                    </div>
                  ))}
                </div>
              )}
              {summaryCandidates.length > 0 && (
                <div>
                  <h4 style={{ fontSize: 14, fontWeight: 700, marginBottom: 8, color: 'var(--ba-accent)' }}>💬 回复建议</h4>
                  {summaryCandidates.map((c: any, i: number) => (
                    <div key={i} style={{ padding: '10px 14px', background: 'var(--ba-bg)', borderRadius: 8, marginBottom: 6, cursor: 'pointer', transition: 'var(--ba-transition)' }} onClick={() => { setShowSummary(false); handleSelectReply(c.content) }} onMouseEnter={(e) => { e.currentTarget.style.background = 'var(--ba-blue-light)' }} onMouseLeave={(e) => { e.currentTarget.style.background = 'var(--ba-bg)' }}>
                      <div style={{ fontSize: 14 }}>{c.content}</div>
                      <div style={{ fontSize: 11, color: 'var(--ba-text-light)', marginTop: 4 }}>
                        置信度: {Math.round((c.confidence || 0) * 100)}% · 点击选用
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </>
          )}
        </div>
      </Modal>

      <Modal open={showReplyCandidates} onClose={() => setShowReplyCandidates(false)} title="智能回复" style={{ maxWidth: 480 }}>
        <div style={{ minHeight: 100 }}>
          {replyLoading ? (
            <div style={{ textAlign: 'center', padding: '24px 0' }}>
              <RefreshCw size={24} className="spin" style={{ margin: '0 auto 12px' }} />
              <p style={{ color: 'var(--ba-text-light)' }}>正在生成回复建议...</p>
            </div>
          ) : replyCandidates.length === 0 ? (
            <p style={{ textAlign: 'center', color: 'var(--ba-text-light)' }}>暂无回复建议</p>
          ) : (
            replyCandidates.map((c: any, i: number) => (
              <div key={i} style={{ padding: '12px 16px', background: 'var(--ba-bg)', borderRadius: 8, marginBottom: 8, cursor: 'pointer', transition: 'var(--ba-transition)' }} onClick={() => handleSelectReply(c.content)} onMouseEnter={(e) => { e.currentTarget.style.background = 'var(--ba-blue-light)' }} onMouseLeave={(e) => { e.currentTarget.style.background = 'var(--ba-bg)' }}>
                <div style={{ fontSize: 14, lineHeight: 1.6 }}>{c.content}</div>
                <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 6 }}>
                  <span style={{ fontSize: 11, color: 'var(--ba-text-light)' }}>置信度: {Math.round((c.confidence || 0) * 100)}%</span>
                  <span style={{ fontSize: 11, color: 'var(--ba-blue)' }}>点击选用 →</span>
                </div>
              </div>
            ))
          )}
        </div>
      </Modal>

      <Modal open={showAddFriend} onClose={() => { setShowAddFriend(false); setAddFriendError(''); setAddFriendId(''); setAddFriendMsg('') }} title="添加好友">
        <div className="new-chat-form">
          <div className="login-field">
            <label>用户ID</label>
            <input className="ba-input" placeholder="输入对方用户ID" value={addFriendId} onChange={(e) => { setAddFriendId(e.target.value); setAddFriendError('') }} onKeyDown={(e) => e.key === 'Enter' && addFriendId.trim() && addFriend(addFriendId.trim(), '', addFriendMsg.trim()).then(res => { if (res?.error) { setAddFriendError(res.error || res.message) } else { setShowAddFriend(false); setAddFriendId(''); setAddFriendMsg('') } }).catch(err => setAddFriendError(err?.response?.data?.error || err?.message || '添加失败'))} />
            {addFriendError && <p style={{ color: 'var(--ba-accent)', fontSize: 12, marginTop: 4 }}>{addFriendError}</p>}
          </div>
          <div className="login-field">
            <label>验证消息（选填）</label>
            <input className="ba-input" placeholder="你好，我是..." value={addFriendMsg} onChange={(e) => setAddFriendMsg(e.target.value)} />
          </div>
          <button className="ba-btn ba-btn-primary" onClick={() => addFriend(addFriendId.trim(), '', addFriendMsg.trim()).then(res => { if (res?.error) { setAddFriendError(res.error || res.message) } else { setShowAddFriend(false); setAddFriendId(''); setAddFriendMsg('') } }).catch(err => setAddFriendError(err?.response?.data?.error || err?.message || '添加失败'))} style={{ marginTop: 16, width: '100%' }}>
            发送申请
          </button>
        </div>
      </Modal>

      <Modal open={showFriendRequests} onClose={() => setShowFriendRequests(false)} title="好友申请">
        <div style={{ minHeight: 100 }}>
          {friendRequestList.length === 0 ? (
            <p style={{ textAlign: 'center', color: 'var(--ba-text-light)' }}>暂无好友申请</p>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
              {friendRequestList.map((req: any) => (
                <div key={req.id || req.Id} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '8px 0', borderBottom: '1px solid var(--ba-border)' }}>
                  <div>
                    <div style={{ fontWeight: 600 }}>用户 {req.from_user_id || req.FromUserId || req.fromUserId}</div>
                    {req.message && <div style={{ fontSize: 12, color: 'var(--ba-text-light)', marginTop: 2 }}>{req.message || req.Message}</div>}
                  </div>
                  <div style={{ display: 'flex', gap: 8 }}>
                    <button className="ba-btn ba-btn-primary" style={{ padding: '4px 12px', fontSize: 12 }} onClick={async () => { await handleFriendRequest(req.id || req.Id, 'accepted'); setFriendRequestList(prev => prev.filter((r: any) => (r.id || r.Id) !== (req.id || req.Id))); refreshConversations() }}>同意</button>
                    <button className="ba-btn" style={{ padding: '4px 12px', fontSize: 12 }} onClick={async () => { await handleFriendRequest(req.id || req.Id, 'rejected'); setFriendRequestList(prev => prev.filter((r: any) => (r.id || r.Id) !== (req.id || req.Id))) }}>拒绝</button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      </Modal>

      <Modal open={showNewChat} onClose={() => { setShowNewChat(false); setNewChatError(''); setNewChatName('') }} title="新建私聊">
        <div className="new-chat-form">
          <div className="login-field">
            <label>用户名 / 用户ID</label>
            <input className="ba-input" placeholder="输入对方用户名或ID" value={newChatName} onChange={(e) => { setNewChatName(e.target.value); setNewChatError('') }} onKeyDown={(e) => e.key === 'Enter' && handleCreateChat()} />
            {newChatError && <p style={{ color: 'var(--ba-accent)', fontSize: 12, marginTop: 4 }}>{newChatError}</p>}
          </div>
          <button className="ba-btn ba-btn-primary" onClick={handleCreateChat} disabled={newChatLoading} style={{ marginTop: 16, width: '100%' }}>
            {newChatLoading ? '查找中...' : '开始对话'}
          </button>
        </div>
      </Modal>

      <Modal open={showBotPanel} onClose={() => setShowBotPanel(false)} title="Bot 列表">
        <div className="bot-list-panel">
          {bots.map((bot) => (
            <div key={bot.id} className="bot-list-item" onClick={() => handleSelectBot(bot)}>
              <div className="ba-avatar ba-avatar-sm bot-avatar"><Bot size={16} /></div>
              <div className="bot-list-item-info"><div className="bot-list-item-name">{bot.name}</div></div>
            </div>
          ))}
          {bots.length === 0 && <div className="chat-list-empty">暂无Bot</div>}
        </div>
      </Modal>

      <Modal open={showEditModal} onClose={() => setShowEditModal(false)} title="编辑消息">
        <div className="new-chat-form">
          <div className="login-field">
            <label>消息内容</label>
            <textarea
              className="ba-input"
              placeholder="编辑消息"
              value={editContent}
              onChange={(e) => setEditContent(e.target.value)}
              style={{ height: 100, resize: 'vertical' }}
            />
          </div>
          <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
            <button
              className="ba-btn ba-btn-secondary"
              style={{ flex: 1 }}
              onClick={() => setShowEditModal(false)}
            >
              取消
            </button>
            <button
              className="ba-btn ba-btn-primary"
              style={{ flex: 1 }}
              onClick={handleSaveEdit}
            >
              保存
            </button>
          </div>
        </div>
      </Modal>

      <Modal open={showForwardModal} onClose={() => setShowForwardModal(false)} title="转发消息" width={800}>
        <div style={{ display: 'flex', gap: 20, minHeight: 400 }}>
          {/* 左侧 - 预览 */}
          <div style={{ width: '40%', borderRight: '1px solid var(--ba-border)', paddingRight: 20 }}>
            <div style={{ fontSize: 14, color: 'var(--ba-text-light)', marginBottom: 12 }}>要转发的消息：</div>
            <div style={{ 
              background: 'var(--ba-bg-tertiary)', 
              padding: 16, 
              borderRadius: 8,
              border: '1px solid var(--ba-border)'
            }}>
              <div style={{ fontSize: 14, fontWeight: 600, marginBottom: 8 }}>
                {forwardingMessage?.senderName}
              </div>
              {forwardingMessage?.messageType !== 'text' && forwardingMessage?.mediaUrl && (
                <div style={{ marginBottom: 8 }}>
                  {forwardingMessage.messageType === 'image' && (
                    <img src={toMediaUrl(forwardingMessage.mediaUrl)} alt="" style={{ maxWidth: '100%', maxHeight: 120, borderRadius: 6, objectFit: 'cover' }} />
                  )}
                  {forwardingMessage.messageType === 'file' && (
                    <div style={{ fontSize: 12, color: 'var(--ba-text-light)' }}>📎 {forwardingMessage.mediaMeta || '文件'}</div>
                  )}
                </div>
              )}
              <div style={{ fontSize: 14, color: 'var(--ba-text)' }}>
                {forwardingMessage?.content}
              </div>
            </div>
          </div>
          
          {/* 右侧 - 选择聊天 */}
          <div style={{ width: '60%' }}>
            <div style={{ fontSize: 14, color: 'var(--ba-text-light)', marginBottom: 12 }}>
              请选择转发到的聊天：
            </div>
            <div style={{ maxHeight: 350, overflow: 'auto', border: '1px solid var(--ba-border)', borderRadius: 8 }}>
              {[...chats]
                .filter(c => c.id !== selectedChatId)
                .sort((a, b) => {
                  const timeA = a.lastMessageTime ? new Date(a.lastMessageTime).getTime() : 0
                  const timeB = b.lastMessageTime ? new Date(b.lastMessageTime).getTime() : 0
                  return timeB - timeA
                })
                .map((chat) => (
                <div
                  key={chat.id}
                  onClick={() => { setForwardChatId(chat.id); setForwardChatType(chat.type) }}
                  style={{ 
                    padding: '12px 16px', 
                    cursor: 'pointer', 
                    borderBottom: '1px solid var(--ba-border)',
                    background: forwardChatId === chat.id ? 'var(--ba-blue-light)' : 'transparent',
                    color: forwardChatId === chat.id ? 'var(--ba-blue)' : 'inherit',
                    fontWeight: forwardChatId === chat.id ? 600 : 400,
                    transition: 'background 0.15s'
                  }}
                >
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
                    <span style={{ fontSize: 14 }}>{chat.name}</span>
                    {chat.type === 'group' && (
                      <span style={{ fontSize: 11, background: 'var(--ba-blue-light)', color: 'var(--ba-blue)', padding: '1px 6px', borderRadius: 4 }}>群聊</span>
                    )}
                  </div>
                  {chat.lastMessage && (
                    <div style={{ fontSize: 12, color: 'var(--ba-text-light)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {chat.lastMessage}
                    </div>
                  )}
                </div>
              ))}
              {chats.filter(c => c.id !== selectedChatId).length === 0 && (
                <div style={{ padding: 24, textAlign: 'center', color: 'var(--ba-text-light)' }}>
                  没有其他聊天
                </div>
              )}
            </div>
            <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
              <button
                className="ba-btn ba-btn-secondary"
                style={{ flex: 1 }}
                onClick={() => setShowForwardModal(false)}
              >
                取消
              </button>
              <button
                className="ba-btn ba-btn-primary"
                style={{ flex: 1 }}
                onClick={handleConfirmForward}
                disabled={!forwardChatId}
              >
                {!forwardChatId ? '请选择聊天' : `转发到${forwardChatType === 'group' ? '群聊' : '私聊'}`}
              </button>
            </div>
          </div>
        </div>
      </Modal>

      <Modal open={showCreateGroup} onClose={() => setShowCreateGroup(false)} title="创建群组">
        <div className="new-chat-form">
          <div className="login-field">
            <label>群组名称</label>
            <input
              className="ba-input"
              placeholder="请输入群组名称"
              value={newGroupName}
              onChange={(e) => setNewGroupName(e.target.value)}
            />
          </div>
          <div className="login-field">
            <label>群组描述（可选）</label>
            <input
              className="ba-input"
              placeholder="请输入群组描述"
              value={newGroupDescription}
              onChange={(e) => setNewGroupDescription(e.target.value)}
            />
          </div>
          <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
            <button
              className="ba-btn ba-btn-secondary"
              style={{ flex: 1 }}
              onClick={() => setShowCreateGroup(false)}
            >
              取消
            </button>
            <button
              className="ba-btn ba-btn-primary"
              style={{ flex: 1 }}
              onClick={handleCreateGroup}
              disabled={createGroupLoading}
            >
              {createGroupLoading ? '创建中...' : '创建群组'}
            </button>
          </div>
        </div>
      </Modal>

      <Modal open={showJoinGroup} onClose={() => { setShowJoinGroup(false); setJoinGroupError(''); setJoinGroupId('') }} title="加入群组">
        <div className="new-chat-form">
          <div className="login-field">
            <label>群组ID</label>
            <input
              className="ba-input"
              placeholder="请输入群组ID"
              value={joinGroupId}
              onChange={(e) => { setJoinGroupId(e.target.value); setJoinGroupError('') }}
              onKeyDown={(e) => e.key === 'Enter' && handleJoinGroup()}
            />
            {joinGroupError && <p style={{ color: 'var(--ba-accent)', fontSize: 12, marginTop: 4 }}>{joinGroupError}</p>}
          </div>
          <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
            <button
              className="ba-btn ba-btn-secondary"
              style={{ flex: 1 }}
              onClick={() => { setShowJoinGroup(false); setJoinGroupError(''); setJoinGroupId('') }}
            >
              取消
            </button>
            <button
              className="ba-btn ba-btn-primary"
              style={{ flex: 1 }}
              onClick={handleJoinGroup}
              disabled={joinLoading}
            >
              {joinLoading ? '加入中...' : '加入群组'}
            </button>
          </div>
        </div>
      </Modal>

      <Modal open={showAddMember} onClose={() => { setShowAddMember(false); setAddMemberError(''); setAddMemberId('') }} title="邀请成员">
        <div className="new-chat-form">
          <div className="login-field">
            <label>用户ID</label>
            <input
              className="ba-input"
              placeholder="请输入用户ID"
              value={addMemberId}
              onChange={(e) => { setAddMemberId(e.target.value); setAddMemberError('') }}
              onKeyDown={(e) => e.key === 'Enter' && handleAddMember()}
            />
            {addMemberError && <p style={{ color: 'var(--ba-accent)', fontSize: 12, marginTop: 4 }}>{addMemberError}</p>}
          </div>
          <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
            <button
              className="ba-btn ba-btn-secondary"
              style={{ flex: 1 }}
              onClick={() => { setShowAddMember(false); setAddMemberError(''); setAddMemberId('') }}
            >
              取消
            </button>
            <button
              className="ba-btn ba-btn-primary"
              style={{ flex: 1 }}
              onClick={handleAddMember}
            >
              邀请
            </button>
          </div>
        </div>
      </Modal>

      <Modal open={showTransferOwner} onClose={() => { setShowTransferOwner(false); setTransferToUserId('') }} title="转让群主">
        <div className="new-chat-form">
          <div style={{ marginBottom: '12px', color: 'var(--ba-text-light)', fontSize: '14px' }}>
            请选择要转让给哪位成员：
          </div>
          <div style={{ maxHeight: '300px', overflowY: 'auto', border: '1px solid var(--ba-border)', borderRadius: '8px', marginBottom: '12px' }}>
            {Array.from(groupMembers.values()).filter((m: any) => m.userId !== user?.id).map((member: any) => (
              <div 
                key={member.userId}
                style={{ 
                  padding: '12px', 
                  display: 'flex', 
                  alignItems: 'center', 
                  gap: '12px',
                  cursor: 'pointer',
                  borderBottom: '1px solid var(--ba-border)',
                  transition: 'background 0.15s',
                  background: transferToUserId === member.userId ? 'var(--ba-blue-light)' : 'transparent'
                }}
                onClick={() => setTransferToUserId(member.userId)}
              >
                <div className="ba-avatar ba-avatar-sm">
                  {member.avatar ? (
                    <img src={toMediaUrl(member.avatar)} alt="" style={{ width: '100%', height: '100%', borderRadius: '50%', objectFit: 'cover' }} />
                  ) : (
                    (member.username || member.userId).charAt(0) || '?'
                  )}
                </div>
                <div style={{ flex: 1 }}>
                  <div style={{ fontSize: '14px', fontWeight: 600 }}>
                    {member.username || `用户 ${member.userId.slice(0, 8)}`}
                  </div>
                  <div style={{ fontSize: '12px', color: 'var(--ba-text-light)' }}>
                    {member.role === 'owner' ? '群主' : member.role === 'admin' ? '管理员' : '成员'}
                  </div>
                </div>
                {transferToUserId === member.userId && <div style={{ color: 'var(--ba-blue)' }}>✓</div>}
              </div>
            ))}
          </div>
          <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
            <button
              className="ba-btn ba-btn-secondary"
              style={{ flex: 1 }}
              onClick={() => { setShowTransferOwner(false); setTransferToUserId('') }}
            >
              取消
            </button>
            <button
              className="ba-btn ba-btn-primary"
              style={{ flex: 1 }}
              onClick={handleTransferOwner}
              disabled={!transferToUserId}
            >
              确认转让
            </button>
          </div>
        </div>
      </Modal>

      <Modal open={showMuteMember} onClose={() => { setShowMuteMember(false); setMuteMemberId(''); setMuteDuration(0) }} title="禁言成员">
        <div className="new-chat-form">
          <div style={{ marginBottom: '12px', color: 'var(--ba-text-light)', fontSize: '14px' }}>
            选择禁言时长：
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
            {[
              { label: '10分钟', value: 10 },
              { label: '30分钟', value: 30 },
              { label: '1小时', value: 60 },
              { label: '6小时', value: 360 },
              { label: '12小时', value: 720 },
              { label: '1天', value: 1440 },
              { label: '永久', value: 0 },
            ].map(opt => (
              <button
                key={opt.value}
                className={`ba-btn ${muteDuration === opt.value ? 'ba-btn-primary' : 'ba-btn-secondary'}`}
                onClick={() => setMuteDuration(opt.value)}
                style={{ width: '100%', textAlign: 'left' }}
              >
                {opt.label}
              </button>
            ))}
          </div>
          <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
            <button className="ba-btn ba-btn-secondary" style={{ flex: 1 }} onClick={() => { setShowMuteMember(false); setMuteMemberId(''); setMuteDuration(0) }}>取消</button>
            <button className="ba-btn ba-btn-primary" style={{ flex: 1 }} onClick={handleMuteMember} disabled={muteDuration === 0 && muteMemberId === ''}>确认禁言</button>
          </div>
        </div>
      </Modal>

      <Modal open={showAnnouncement} onClose={() => setShowAnnouncement(false)} title="编辑群公告">
        <div className="new-chat-form">
          <div className="login-field">
            <label>公告内容</label>
            <textarea
              className="ba-input"
              placeholder="输入群公告..."
              value={announcementText}
              onChange={(e) => setAnnouncementText(e.target.value)}
              rows={6}
              style={{ resize: 'vertical' }}
            />
          </div>
          <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
            <button className="ba-btn ba-btn-secondary" style={{ flex: 1 }} onClick={() => setShowAnnouncement(false)}>取消</button>
            <button className="ba-btn ba-btn-primary" style={{ flex: 1 }} onClick={handleUpdateAnnouncement}>保存公告</button>
          </div>
        </div>
      </Modal>

      <Modal open={showSetAdmin} onClose={() => { setShowSetAdmin(false); setSetAdminMemberId('') }} title={setAdminIsAdmin ? '设为管理员' : '取消管理员'}>
        <div className="new-chat-form">
          <div style={{ marginBottom: '12px', color: 'var(--ba-text-light)', fontSize: '14px' }}>
            {setAdminIsAdmin ? '确认将该成员设为管理员？' : '确认取消该成员的管理员身份？'}
          </div>
          <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
            <button className="ba-btn ba-btn-secondary" style={{ flex: 1 }} onClick={() => { setShowSetAdmin(false); setSetAdminMemberId('') }}>取消</button>
            <button className="ba-btn ba-btn-primary" style={{ flex: 1 }} onClick={handleSetGroupAdmin}>确认</button>
          </div>
        </div>
      </Modal>

      <Modal open={showInviteBot} onClose={() => { setShowInviteBot(false); setInviteBotId('') }} title="邀请Bot加入群聊">
        <div className="new-chat-form">
          <div style={{ marginBottom: '12px', color: 'var(--ba-text-light)', fontSize: '14px' }}>
            选择要邀请的Bot：
          </div>
          <div style={{ maxHeight: '300px', overflowY: 'auto' }}>
            {bots.map((bot) => {
              const botMemberId = `bot_${bot.id}`
              const alreadyInGroup = groupMembers.has(botMemberId)
              return (
                <div
                  key={bot.id}
                  style={{
                    padding: '12px',
                    display: 'flex',
                    alignItems: 'center',
                    gap: '12px',
                    borderBottom: '1px solid var(--ba-border)',
                    opacity: alreadyInGroup ? 0.5 : 1
                  }}
                >
                  <div className="ba-avatar ba-avatar-sm bot-avatar">
                    {bot.avatar ? <img src={toMediaUrl(bot.avatar)} alt="" style={{ width: '100%', height: '100%', borderRadius: '50%', objectFit: 'cover' }} /> : <Bot size={16} />}
                  </div>
                  <div style={{ flex: 1 }}>
                    <div style={{ fontSize: '14px', fontWeight: 600 }}>{bot.name}</div>
                    <div style={{ fontSize: '12px', color: 'var(--ba-text-light)' }}>{(bot as any).description || '暂无描述'}</div>
                  </div>
                  <button
                    className="ba-btn ba-btn-primary"
                    style={{ padding: '4px 12px', fontSize: 12 }}
                    disabled={alreadyInGroup}
                    onClick={() => { setInviteBotId(bot.id) }}
                  >
                    {alreadyInGroup ? '已加入' : '邀请'}
                  </button>
                </div>
              )
            })}
          </div>
          {bots.length === 0 && (
            <div style={{ textAlign: 'center', color: 'var(--ba-text-light)', padding: '24px 0' }}>
              暂无可用的Bot
            </div>
          )}
          <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
            <button className="ba-btn ba-btn-secondary" style={{ flex: 1 }} onClick={() => { setShowInviteBot(false); setInviteBotId('') }}>取消</button>
            <button className="ba-btn ba-btn-primary" style={{ flex: 1 }} onClick={handleInviteBot} disabled={!inviteBotId}>确认邀请</button>
          </div>
        </div>
      </Modal>
    </div>
  )
}
