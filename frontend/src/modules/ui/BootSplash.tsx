import { useEffect, useRef, useState } from 'react';
import { useReducedMotion } from 'motion/react';
import { Wordmark } from '../productChrome/Wordmark';

const messages = ['Fluffing the virtual cushions…', 'Untangling the plot twists…', 'Giving your favourites the best seats…', 'Checking behind the sofa for sequels…', 'Keeping the good stuff close to home…'];

export function BootSplash({ ready, onComplete }: { ready: boolean; onComplete: () => void }) {
  const [message, setMessage] = useState(0);
  const [leaving, setLeaving] = useState(false);
  const reduced = useReducedMotion();
  const started = useRef(performance.now());
  useEffect(() => {
    const timer = window.setInterval(() => setMessage((value) => (value + 1) % messages.length), 1900);
    return () => window.clearInterval(timer);
  }, []);
  useEffect(() => {
    if (!ready) return;
    let active = true;
    let revealTimer = 0;
    let finishTimer = 0;
    // Bootstrap data is sufficient: remote artwork is optional and must not hold
    // profile selection behind a splash when it is slow or unavailable.
    revealTimer = window.setTimeout(() => {
      if (!active) return;
      setLeaving(true);
      finishTimer = window.setTimeout(onComplete, reduced ? 0 : 480);
    }, reduced ? 0 : Math.max(0, 350 - (performance.now() - started.current)));
    return () => { active = false; window.clearTimeout(revealTimer); window.clearTimeout(finishTimer); };
  }, [ready, reduced, onComplete]);
  return <div className={`boot-splash ${leaving ? 'is-leaving' : ''}`} role="status" aria-label={leaving ? 'Your cinema is ready' : 'Preparing your cinema'}><div className="boot-beams" aria-hidden="true">{Array.from({ length: 9 }, (_, i) => <span key={i} style={{ '--beam': i } as React.CSSProperties} />)}</div><div className="boot-brand"><Wordmark /></div><span className="splash-line" aria-hidden="true" /><p key={message} className="boot-message" aria-hidden="true">{messages[message]}</p><span className="boot-local">YOUR CINEMA. RIGHT HERE.</span></div>;
}
