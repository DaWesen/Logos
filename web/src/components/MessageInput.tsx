import { useState, useRef, useEffect, KeyboardEvent } from 'react'
import { Send, Paperclip, Smile, AtSign } from 'lucide-react'
import './MessageInput.css'

const EMOJI_LIST = [
  '😀','😂','🤣','😊','😍','🥰','😘','😎','🤔','😏',
  '😢','😭','😤','🤯','😱','🥳','😴','🤮','👍','👎',
  '👏','🙏','💪','❤️','🔥','⭐','🎉','🎊','💯','✅',
  '❌','⚡','🌈','🌸','🍀','🐱','🐶','🦊','🐼','🤖',
]

interface Props {
  onSend: (content: string) => void
  onUpload?: (file: File) => void
  onAtBot?: () => void
  onTyping?: () => void
  placeholder?: string
  disabled?: boolean
  bots?: { id: string; name: string }[]
  onMentionBot?: (botId: string, botName: string) => void
}

export default function MessageInput({ onSend, onUpload, onAtBot, onTyping, placeholder = '输入消息...', disabled, bots, onMentionBot }: Props) {
  const [text, setText] = useState('')
  const [showEmoji, setShowEmoji] = useState(false)
  const [emojiPos, setEmojiPos] = useState<{ top: number; left: number } | null>(null)
  const [showBotPicker, setShowBotPicker] = useState(false)
  const [botFilter, setBotFilter] = useState('')
  const fileRef = useRef<HTMLInputElement>(null)
  const emojiBtnRef = useRef<HTMLButtonElement>(null)
  const emojiPickerRef = useRef<HTMLDivElement>(null)
  const botPickerRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const typingTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const triggerTyping = () => {
    if (!onTyping) return
    if (typingTimerRef.current) return
    onTyping()
    typingTimerRef.current = setTimeout(() => {
      typingTimerRef.current = null
    }, 3000)
  }

  const handleSend = () => {
    const trimmed = text.trim()
    if (!trimmed || disabled) return
    onSend(trimmed)
    setText('')
    setShowBotPicker(false)
  }

  const handleKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSend()
    }
  }

  const handleFileChange = () => {
    const file = fileRef.current?.files?.[0]
    if (file && onUpload) {
      onUpload(file)
      fileRef.current.value = ''
    }
  }

  const handleEmojiClick = (emoji: string) => {
    setText((prev) => prev + emoji)
  }

  const toggleEmoji = () => {
    if (showEmoji) {
      setShowEmoji(false)
      setEmojiPos(null)
      return
    }
    if (emojiBtnRef.current) {
      const rect = emojiBtnRef.current.getBoundingClientRect()
      setEmojiPos({
        top: rect.top - 8,
        left: rect.left + rect.width / 2,
      })
    }
    setShowEmoji(true)
  }

  const handleTextChange = (value: string) => {
    setText(value)
    triggerTyping()

    if (bots && bots.length > 0 && onMentionBot) {
      const cursorPos = textareaRef.current?.selectionStart ?? value.length
      const textBeforeCursor = value.slice(0, cursorPos)
      const atMatch = textBeforeCursor.match(/@([^\s@]*)$/)
      if (atMatch) {
        setBotFilter(atMatch[1])
        setShowBotPicker(true)
      } else {
        setShowBotPicker(false)
      }
    }
  }

  const handleSelectBot = (bot: { id: string; name: string }) => {
    const cursorPos = textareaRef.current?.selectionStart ?? text.length
    const textBeforeCursor = text.slice(0, cursorPos)
    const textAfterCursor = text.slice(cursorPos)
    const newTextBefore = textBeforeCursor.replace(/@([^\s@]*)$/, `@${bot.name} `)
    const newText = newTextBefore + textAfterCursor
    setText(newText)
    setShowBotPicker(false)
    setBotFilter('')
    if (onMentionBot) {
      onMentionBot(bot.id, bot.name)
    }
    setTimeout(() => {
      const newCursorPos = newTextBefore.length
      textareaRef.current?.focus()
      textareaRef.current?.setSelectionRange(newCursorPos, newCursorPos)
    }, 0)
  }

  const filteredBots = bots && botFilter
    ? bots.filter(b => b.name.toLowerCase().includes(botFilter.toLowerCase()))
    : bots || []

  useEffect(() => {
    if (!showEmoji) return
    const handleClick = (e: MouseEvent) => {
      if (
        emojiPickerRef.current &&
        !emojiPickerRef.current.contains(e.target as Node) &&
        emojiBtnRef.current &&
        !emojiBtnRef.current.contains(e.target as Node)
      ) {
        setShowEmoji(false)
        setEmojiPos(null)
      }
    }
    const handleScroll = () => {
      if (showEmoji && emojiBtnRef.current) {
        const rect = emojiBtnRef.current.getBoundingClientRect()
        setEmojiPos({
          top: rect.top - 8,
          left: rect.left + rect.width / 2,
        })
      }
    }
    document.addEventListener('mousedown', handleClick)
    document.addEventListener('scroll', handleScroll, true)
    return () => {
      document.removeEventListener('mousedown', handleClick)
      document.removeEventListener('scroll', handleScroll, true)
    }
  }, [showEmoji])

  useEffect(() => {
    if (!showBotPicker) return
    const handleClick = (e: MouseEvent) => {
      if (
        botPickerRef.current &&
        !botPickerRef.current.contains(e.target as Node)
      ) {
        setShowBotPicker(false)
        setBotFilter('')
      }
    }
    document.addEventListener('mousedown', handleClick)
    return () => {
      document.removeEventListener('mousedown', handleClick)
    }
  }, [showBotPicker])

  return (
    <div className="message-input-wrapper">
      <div className="message-input-actions">
        <button className="input-action-btn" onClick={() => fileRef.current?.click()} title="上传文件">
          <Paperclip size={18} />
        </button>
        <input ref={fileRef} type="file" hidden onChange={handleFileChange} />
        {onAtBot && (
          <button className="input-action-btn" onClick={onAtBot} title="@Bot">
            <AtSign size={18} />
          </button>
        )}
        <button
          ref={emojiBtnRef}
          className={`input-action-btn ${showEmoji ? 'active' : ''}`}
          onClick={toggleEmoji}
          title="表情"
        >
          <Smile size={18} />
        </button>
      </div>
      <div className="message-input-field" style={{ position: 'relative' }}>
        <textarea
          ref={textareaRef}
          value={text}
          onChange={(e) => handleTextChange(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder={placeholder}
          disabled={disabled}
          rows={1}
        />
        {showBotPicker && filteredBots.length > 0 && (
          <div ref={botPickerRef} className="bot-mention-picker">
            {filteredBots.map((bot) => (
              <button
                key={bot.id}
                className="bot-mention-item"
                onClick={() => handleSelectBot(bot)}
              >
                🤖 {bot.name}
              </button>
            ))}
          </div>
        )}
      </div>
      <button className="message-input-send" onClick={handleSend} disabled={disabled || !text.trim()}>
        <Send size={18} />
      </button>
      {showEmoji && emojiPos && (
        <div
          ref={emojiPickerRef}
          className="emoji-picker"
          style={{
            position: 'fixed',
            transform: 'translate(-50%, -100%)',
            top: emojiPos.top,
            left: emojiPos.left,
          }}
        >
          {EMOJI_LIST.map((emoji) => (
            <button key={emoji} className="emoji-item" onClick={() => handleEmojiClick(emoji)}>
              {emoji}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
