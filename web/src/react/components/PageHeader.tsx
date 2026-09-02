import type { ReactNode } from 'react'
import { Typography } from 'antd'

interface Props {
  title: ReactNode
  description?: ReactNode
  extra?: ReactNode
  eyebrow?: ReactNode
}

export default function PageHeader({ title, description, extra, eyebrow }: Props) {
  return (
    <header className="page-header">
      <div className="page-header-copy">
        {eyebrow && <div className="page-eyebrow">{eyebrow}</div>}
        <Typography.Title level={2}>{title}</Typography.Title>
        {description && <Typography.Paragraph type="secondary">{description}</Typography.Paragraph>}
      </div>
      {extra && <div className="page-header-extra">{extra}</div>}
    </header>
  )
}
