import { useEffect, useMemo, useRef, useState } from 'react';
import { useFrame } from '@react-three/fiber';
import { Html } from '@react-three/drei';
import * as THREE from 'three';
import useWebSocket from '../../hooks/useWebSocket';
import { getSyncedTimeSeconds } from '../../utils/timeSync';
import { openCase } from '../../services/fraudApi';
import { fmtAmount } from './riskFormat';

// Same orbits as backend/internal/engine/physics.go and Planet.jsx.
const ORBITS = {
  mercury: { distance: 39, speed: 0.82, radius: 2.5 },
  venus: { distance: 72, speed: 0.32, radius: 4.5 },
  earth: { distance: 100, speed: 0.2, radius: 5 },
  mars: { distance: 152, speed: 0.11, radius: 3.5 },
  jupiter: { distance: 520, speed: 0.017, radius: 12 },
  saturn: { distance: 954, speed: 0.0067, radius: 10 },
  uranus: { distance: 1919, speed: 0.0024, radius: 7 },
  neptune: { distance: 3007, speed: 0.0012, radius: 7 },
};

function planetPosition(name, out = new THREE.Vector3()) {
  const o = ORBITS[(name || '').toLowerCase()];
  if (!o) return out.set(0, 0, 0);
  const a = (getSyncedTimeSeconds() * o.speed) % (2 * Math.PI);
  return out.set(o.distance * Math.cos(a), 0, o.distance * Math.sin(a));
}

// Held packets wait on the "launch pad" above their sender's planet.
function HeldPad({ planet, cases }) {
  const group = useRef();
  const ring = useRef();
  const r = (ORBITS[planet]?.radius || 5) * 2 + 6;
  useFrame(({ clock }) => {
    if (!group.current) return;
    planetPosition(planet, group.current.position);
    group.current.position.y = r * 0.6;
    if (ring.current) {
      const pulse = 1 + 0.12 * Math.sin(clock.elapsedTime * 4);
      ring.current.scale.set(pulse, pulse, pulse);
      ring.current.rotation.z += 0.01;
    }
  });
  return (
    <group ref={group}>
      <mesh ref={ring} rotation={[Math.PI / 2, 0, 0]}>
        <torusGeometry args={[r, Math.max(0.6, r * 0.06), 12, 64]} />
        <meshBasicMaterial color="#d8505c" transparent opacity={0.85} />
      </mesh>
      {cases.slice(0, 6).map((c, i) => (
        <mesh key={c.tx_id} position={[Math.cos(i * 1.05) * r * 0.55, 1 + i * 0.4, Math.sin(i * 1.05) * r * 0.55]} onClick={() => openCase(c.tx_id)}>
          <icosahedronGeometry args={[Math.max(1.5, r * 0.12), 1]} />
          <meshStandardMaterial color="#d8505c" emissive="#5a1016" emissiveIntensity={0.6} />
        </mesh>
      ))}
      <Html center position={[0, r * 0.9, 0]} zIndexRange={[5, 0]}>
        <div className="held-label" onClick={() => openCase(cases[0].tx_id)}>
          <b>{cases.length} held</b>
          <span>
            @{cases[0].sender} · {fmtAmount(cases[0].amount)} {cases[0].currency}
          </span>
        </div>
      </Html>
    </group>
  );
}

// Deterministic pseudo-random numbers seeded by the transaction id (keeps render pure).
function seededRandom(seedText) {
  let h = 2166136261;
  for (let i = 0; i < seedText.length; i++) h = Math.imul(h ^ seedText.charCodeAt(i), 16777619);
  return () => {
    h = Math.imul(h ^ (h >>> 15), 2246822507);
    h = Math.imul(h ^ (h >>> 13), 3266489909);
    return ((h ^= h >>> 16) >>> 0) / 4294967296;
  };
}

// A blocked packet breaks apart into particles over its sender's planet.
function Burst({ id, planet, onDone }) {
  const points = useRef();
  const born = useRef(null);
  const { geometry, velocities } = useMemo(() => {
    const n = 160;
    const g = new THREE.BufferGeometry();
    const pos = new Float32Array(n * 3);
    const vel = [];
    const rand = seededRandom(id);
    const origin = planetPosition(planet);
    origin.y = (ORBITS[planet]?.radius || 5) * 1.5;
    for (let i = 0; i < n; i++) {
      pos.set([origin.x, origin.y, origin.z], i * 3);
      const u = rand() * 2 - 1;
      const th = rand() * Math.PI * 2;
      const k = Math.sqrt(1 - u * u);
      vel.push(new THREE.Vector3(k * Math.cos(th), u, k * Math.sin(th)).multiplyScalar(10 + rand() * 30));
    }
    g.setAttribute('position', new THREE.BufferAttribute(pos, 3));
    return { geometry: g, velocities: vel };
  }, [id, planet]);
  useEffect(() => () => geometry.dispose(), [geometry]);
  useFrame(({ clock }, delta) => {
    if (born.current == null) born.current = clock.elapsedTime;
    const age = clock.elapsedTime - born.current;
    const attr = geometry.getAttribute('position');
    for (let i = 0; i < velocities.length; i++) {
      attr.array[i * 3] += velocities[i].x * delta;
      attr.array[i * 3 + 1] += velocities[i].y * delta;
      attr.array[i * 3 + 2] += velocities[i].z * delta;
    }
    attr.needsUpdate = true;
    if (points.current) points.current.material.opacity = Math.max(0, 1 - age / 1.8);
    if (age > 1.8) onDone();
  });
  return (
    <points ref={points} geometry={geometry}>
      <pointsMaterial color="#d8505c" size={2.2} transparent opacity={1} depthWrite={false} />
    </points>
  );
}

export default function RiskOverlay3D() {
  const { state } = useWebSocket(true);
  const held = useMemo(() => state?.held || [], [state?.held]);
  const alerts = useMemo(() => state?.alerts || [], [state?.alerts]);
  const seenBlocked = useRef(new Set());
  const primed = useRef(false);
  const [bursts, setBursts] = useState([]);

  const byPlanet = useMemo(() => {
    const m = {};
    held.forEach((c) => {
      const p = (c.origin_planet || '').toLowerCase();
      (m[p] = m[p] || []).push(c);
    });
    return m;
  }, [held]);

  useEffect(() => {
    if (!primed.current) {
      // Blocks that happened before this page loaded do not explode again.
      if (!state) return;
      alerts.forEach((a) => a.status === 'blocked' && seenBlocked.current.add(a.tx_id));
      primed.current = true;
      return;
    }
    const fresh = alerts.filter((a) => a.status === 'blocked' && !seenBlocked.current.has(a.tx_id));
    if (!fresh.length) return;
    fresh.forEach((a) => seenBlocked.current.add(a.tx_id));
    setBursts((b) => [...b, ...fresh.map((a) => ({ id: a.tx_id, planet: (a.origin_planet || '').toLowerCase() }))].slice(-12));
  }, [alerts, state]);

  return (
    <>
      {Object.entries(byPlanet).map(([planet, cases]) => (
        <HeldPad key={planet} planet={planet} cases={cases} />
      ))}
      {bursts.map((b) => (
        <Burst key={b.id} id={b.id} planet={b.planet} onDone={() => setBursts((all) => all.filter((x) => x.id !== b.id))} />
      ))}
    </>
  );
}
