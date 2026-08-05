import type { ReactNode } from 'react'

interface PageHeadingProps {
  eyebrow: string
  title: string
  action?: ReactNode
}

export function PageHeading({ eyebrow, title, action }: PageHeadingProps) {
  return (
    <div className="page-heading">
      <div>
        <div className="eyebrow">{eyebrow}</div>
        <h1>{title}</h1>
      </div>
      {action}
    </div>
  )
}
