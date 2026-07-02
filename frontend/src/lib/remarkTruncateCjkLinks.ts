import { visit } from 'unist-util-visit'
import type { Root, Link, Text } from 'mdast'

/**
 * remark-gfm 的 autolink-literal 只在 ASCII 空白/`<` 处终止裸 URL，
 * 因此 URL 后面紧跟的 CJK 字符和全角标点（如 `），包含...`）会被吞进链接。
 *
 * 本插件在 gfm 解析之后运行：把「可见文本本身就是 URL」的 autolink 节点，
 * 在第一个非 ASCII 字符处截断，多出来的部分还原成普通文本兄弟节点。
 * 不处理显式链接 `[中文](url)`（其可见文本不以 http/www 开头）。
 */
export function remarkTruncateCjkLinks() {
  return (tree: Root) => {
    visit(tree, 'link', (node: Link, index, parent) => {
      if (!parent || index == null) return
      if (node.children.length !== 1) return
      const child = node.children[0]
      if (child.type !== 'text') return

      const text = child.value
      // 仅处理 autolink literal：可见文本本身就是一个裸 URL
      if (!/^(https?:\/\/|www\.)/i.test(text)) return

      const m = /[^\x00-\x7F]/.exec(text)
      if (!m || m.index === 0) return

      const cut = m.index
      const linkText = text.slice(0, cut)
      const rest = text.slice(cut)

      child.value = linkText
      node.url = /^www\./i.test(linkText) ? `http://${linkText}` : linkText

      const restNode: Text = { type: 'text', value: rest }
      parent.children.splice(index + 1, 0, restNode)

      // 跳过新插入的文本节点
      return index + 2
    })
  }
}
