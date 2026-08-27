import Editor, { DiffEditor, loader } from '@monaco-editor/react'
import * as monaco from 'monaco-editor/esm/vs/editor/editor.api.js'
import 'monaco-editor/esm/vs/basic-languages/yaml/yaml.contribution.js'
import 'monaco-editor/esm/vs/language/json/monaco.contribution.js'
import EditorWorker from 'monaco-editor/esm/vs/editor/editor.worker?worker'
import JSONWorker from 'monaco-editor/esm/vs/language/json/json.worker?worker'

type MonacoEnvironment = {
  getWorker: (_moduleId: string, label: string) => Worker
}

;(self as unknown as { MonacoEnvironment: MonacoEnvironment }).MonacoEnvironment = {
  getWorker: (_moduleId, label) => {
    if (label === 'json') return new JSONWorker()
    return new EditorWorker()
  },
}

loader.config({ monaco })

interface Props {
  value: string
  height: string
  language: 'json' | 'yaml'
  onChange: (value?: string) => void
  readOnly?: boolean
}

export function JsonEditor({ value, height, language, onChange, readOnly = false }: Props) {
  return (
    <Editor
      height={height}
      language={language}
      theme="vs-dark"
      value={value}
      onChange={onChange}
      options={{
        minimap: { enabled: false },
        fontSize: 14,
        formatOnPaste: true,
        automaticLayout: true,
        scrollBeyondLastLine: false,
        tabSize: 2,
        readOnly,
      }}
    />
  )
}

export function ConfigDiffEditor({ original, modified, height, language }: { original: string; modified: string; height: string; language: 'json' | 'yaml' }) {
  return <DiffEditor height={height} language={language} original={original} modified={modified} theme="vs-dark" options={{ readOnly: true, renderSideBySide: true, minimap: { enabled: false }, automaticLayout: true, fontSize: 13 }} />
}
