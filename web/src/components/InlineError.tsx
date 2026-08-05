export function InlineError({ error }: { error: unknown }) {
  const message = error instanceof Error ? error.message : '操作失败'
  return (
    <div className="inline-error" role="alert">
      {message}
    </div>
  )
}
