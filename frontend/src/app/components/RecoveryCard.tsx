import type { ReactNode } from 'react'

/**
 * Frame of the three public recovery pages (F15, D11): one card on the cream
 * background, the same tokens as the invitation page. `titleId` links the heading
 * to the section for screen readers.
 */
export function RecoveryCard({
  eyebrow,
  title,
  children,
}: {
  eyebrow: string
  title: string
  children: ReactNode
}) {
  return (
    <main className="recovery">
      <section className="recovery__card" aria-labelledby="recovery-title">
        <p className="eyebrow">{eyebrow}</p>
        <h1 id="recovery-title">{title}</h1>
        {children}
      </section>
    </main>
  )
}
