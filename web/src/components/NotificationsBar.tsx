import { useEventStream } from '../lib/events';

export function NotificationsBar() {
  const { connected, notifications, dismiss } = useEventStream();
  const recent = notifications.slice(-5).reverse();

  return (
    <aside className="notifications">
      <span className={`conn conn-${connected ? 'on' : 'off'}`}>{connected ? 'live' : 'reconnecting…'}</span>
      <div className="notif-list">
        {recent.map((n) => (
          <div key={n.id} className={`notif notif-${n.level}`}>
            <span className="notif-msg">{n.message}</span>
            <button type="button" className="notif-x" aria-label="dismiss" onClick={() => dismiss(n.id)}>
              ×
            </button>
          </div>
        ))}
      </div>
    </aside>
  );
}
