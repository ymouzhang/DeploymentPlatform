import { useRef, useState, type CSSProperties, type PointerEvent as ReactPointerEvent, type RefObject } from 'react'

type Point = { x: number; y: number }

type DragState = {
  pointerId: number
  clientX: number
  clientY: number
  origin: Point
  minX: number
  maxX: number
  minY: number
  maxY: number
}

export function useDraggablePanel(): {
  panelRef: RefObject<HTMLDivElement | null>
  panelStyle: CSSProperties
  handleProps: {
    onPointerDown: (event: ReactPointerEvent<HTMLSpanElement>) => void
    onPointerMove: (event: ReactPointerEvent<HTMLSpanElement>) => void
    onPointerUp: (event: ReactPointerEvent<HTMLSpanElement>) => void
    onPointerCancel: (event: ReactPointerEvent<HTMLSpanElement>) => void
  }
} {
  const panelRef = useRef<HTMLDivElement>(null)
  const drag = useRef<DragState | undefined>(undefined)
  const [position, setPosition] = useState<Point>({ x: 0, y: 0 })

  const finish = (event: ReactPointerEvent<HTMLSpanElement>) => {
    if (drag.current?.pointerId !== event.pointerId) return
    event.currentTarget.releasePointerCapture?.(event.pointerId)
    drag.current = undefined
  }

  return {
    panelRef,
    panelStyle: { transform: `translate3d(${position.x}px, ${position.y}px, 0)` },
    handleProps: {
      onPointerDown: (event) => {
        if (event.button !== 0 || !panelRef.current) return
        const rect = panelRef.current.getBoundingClientRect()
        drag.current = {
          pointerId: event.pointerId,
          clientX: event.clientX,
          clientY: event.clientY,
          origin: position,
          minX: position.x - rect.left,
          maxX: position.x + window.innerWidth - rect.right,
          minY: position.y - rect.top,
          maxY: position.y + window.innerHeight - rect.bottom,
        }
        event.currentTarget.setPointerCapture?.(event.pointerId)
        event.preventDefault()
      },
      onPointerMove: (event) => {
        const current = drag.current
        if (!current || current.pointerId !== event.pointerId) return
        const x = Math.min(current.maxX, Math.max(current.minX, current.origin.x + event.clientX - current.clientX))
        const y = Math.min(current.maxY, Math.max(current.minY, current.origin.y + event.clientY - current.clientY))
        setPosition({ x, y })
      },
      onPointerUp: finish,
      onPointerCancel: finish,
    },
  }
}
