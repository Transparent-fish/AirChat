<script setup lang="ts">
import { ref, onMounted, nextTick } from 'vue'

// 消息结构
interface Message {
  sender: string
  content: string
  time: string
  type: 'user' | 'system'
}

const messages = ref<Message[]>([])
const inputMsg = ref('')
const socket = ref<WebSocket | null>(null)
const chatContainer = ref<HTMLElement | null>(null)

// 自动滚动到底部
const scrollToBottom = async () => {
  await nextTick()
  if (chatContainer.value) {
    chatContainer.value.scrollTop = chatContainer.value.scrollHeight
  }
}

// 初始化连接
onMounted(() => {
  socket.value = new WebSocket('ws://localhost:8080/ws')

  socket.value.onmessage = (event) => {
    const data = JSON.parse(event.data)
    messages.value.push(data)
    scrollToBottom()
  }

  socket.value.onclose = () => {
    messages.value.push({
      sender: 'System',
      content: '与服务器断开连接...',
      time: new Date().toLocaleTimeString(),
      type: 'system'
    })
  }
})

// 发送消息
const sendMessage = () => {
  if (!inputMsg.value.trim() || !socket.value) return
  
  socket.value.send(inputMsg.value)
  inputMsg.value = '' // 清空输入框
}
</script>

<template>
  <div class="flex flex-col h-screen bg-gray-100 font-sans">
    <header class="bg-white shadow-sm p-4 text-center font-bold text-gray-700">
      🚀 AirChat 实时实验室
    </header>

    <main 
      ref="chatContainer"
      class="flex-1 overflow-y-auto p-4 space-y-4 scroll-smooth"
    >
      <div v-for="(msg, index) in messages" :key="index">
        <div v-if="msg.type === 'system'" class="flex justify-center">
          <span class="bg-gray-200 text-gray-500 text-xs px-2 py-1 rounded-full">
            {{ msg.content }}
          </span>
        </div>

        <div v-else class="flex flex-col space-y-1">
          <div class="text-xs text-gray-400 px-1">
            {{ msg.sender }} <span class="ml-2">{{ msg.time }}</span>
          </div>
          <div 
            class="max-w-[80%] px-4 py-2 rounded-2xl shadow-sm text-sm"
            :class="msg.type === 'user' ? 'bg-blue-500 text-white rounded-tl-none self-start' : ''"
          >
            {{ msg.content }}
          </div>
        </div>
      </div>
    </main>

    <footer class="p-4 bg-white border-t flex gap-2">
      <input 
        v-model="inputMsg"
        @keyup.enter="sendMessage"
        type="text" 
        placeholder="输入消息..."
        class="flex-1 border rounded-full px-4 py-2 focus:outline-none focus:ring-2 focus:ring-blue-400"
      />
      <button 
        @click="sendMessage"
        class="bg-blue-500 text-white px-6 py-2 rounded-full hover:bg-blue-600 transition-colors"
      >
        发送
      </button>
    </footer>
  </div>
</template>

<style>
/* 简单的滚动条美化 */
::-webkit-scrollbar {
  width: 6px;
}
::-webkit-scrollbar-thumb {
  background-color: #d1d5db;
  border-radius: 10px;
}
</style>