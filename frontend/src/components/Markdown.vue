<script setup lang="ts">
import { computed } from 'vue'
import MarkdownIt from 'markdown-it'
// @ts-ignore
import mk from 'markdown-it-katex'
import 'katex/dist/katex.min.css'

const props = defineProps<{
  content: string
}>()

const md = new MarkdownIt({
  html: true,
  linkify: true,
  typographer: true
}).use(mk)

const renderedContent = computed(() => {
  return md.render(props.content)
})
</script>

<template>
  <div class="markdown-body" v-html="renderedContent"></div>
</template>

<style>
.markdown-body {
  word-break: break-word;
}
.markdown-body p {
  margin-bottom: 0.5rem;
}
.markdown-body p:last-child {
  margin-bottom: 0;
}
.markdown-body pre {
  background: rgba(0,0,0,0.05);
  padding: 0.5rem;
  border-radius: 0.5rem;
  overflow-x: auto;
}
.markdown-body code {
  font-family: monospace;
  background: rgba(0,0,0,0.05);
  padding: 0.1rem 0.3rem;
  border-radius: 0.2rem;
}
</style>
