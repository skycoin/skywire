// Data flow animation: particle system for visualizing bandwidth

import * as S from './state';
import { setFlowParticles, setFlowAnimationId } from './state';

let flowCanvas: HTMLCanvasElement;
let flowCtx: CanvasRenderingContext2D;

class FlowParticle {
    fromNode: string;
    toNode: string;
    progress: number;
    speed: number;
    color: string;
    size: number;
    opacity: number;

    constructor(fromNode: string, toNode: string, speed?: number, color?: string, size?: number) {
        this.fromNode = fromNode;
        this.toNode = toNode;
        this.progress = 0;
        this.speed = speed || 0.02;
        this.color = color || '#00ffff';
        this.size = size || 3;
        this.opacity = 1;
    }

    update(): boolean {
        this.progress += this.speed;
        if (this.progress >= 1) {
            return false;
        }
        return true;
    }

    draw(ctx: CanvasRenderingContext2D, fromPos: { x: number; y: number }, toPos: { x: number; y: number }): void {
        if (!fromPos || !toPos) return;

        const x = fromPos.x + (toPos.x - fromPos.x) * this.progress;
        const y = fromPos.y + (toPos.y - fromPos.y) * this.progress;

        ctx.beginPath();
        ctx.arc(x, y, this.size, 0, Math.PI * 2);
        ctx.fillStyle = this.color;
        ctx.globalAlpha = this.opacity * (1 - this.progress * 0.3);
        ctx.fill();
        ctx.globalAlpha = 1;

        // Add glow effect
        ctx.beginPath();
        ctx.arc(x, y, this.size * 2, 0, Math.PI * 2);
        const gradient = ctx.createRadialGradient(x, y, 0, x, y, this.size * 2);
        gradient.addColorStop(0, this.color + '80');
        gradient.addColorStop(1, this.color + '00');
        ctx.fillStyle = gradient;
        ctx.fill();
    }
}

function resizeFlowCanvas(): void {
    const networkEl = document.getElementById('network')!;
    flowCanvas.width = networkEl.offsetWidth;
    flowCanvas.height = networkEl.offsetHeight;
}

function networkToCanvas(nodeId: string): { x: number; y: number } | null {
    if (!S.network) return null;
    try {
        const pos = S.network.getPosition(nodeId);
        if (!pos) return null;
        const canvasPos = S.network.canvasToDOM(pos);
        return canvasPos;
    } catch (e) {
        return null;
    }
}

function spawnFlowParticles(): void {
    if (!S.localVisorData || !S.localVisorData.connected || !S.network) return;

    const showDataflow = (document.getElementById('show-dataflow') as HTMLInputElement)?.checked;
    if (!showDataflow) return;

    const localPK = S.localVisorData.pub_key;
    const transports = S.localVisorData.transports || [];

    transports.forEach(tp => {
        const sentDelta = tp.sent_delta || 0;
        const recvDelta = tp.recv_delta || 0;
        const hasSentData = tp.sent_bytes > 0;
        const hasRecvData = tp.recv_bytes > 0;

        let sentCount = 0, recvCount = 0;

        if (sentDelta > 0) {
            sentCount = Math.min(5, Math.floor(sentDelta / 10240) + 1);
        } else if (hasSentData && Math.random() < 0.2) {
            sentCount = 1;
        }

        if (recvDelta > 0) {
            recvCount = Math.min(5, Math.floor(recvDelta / 10240) + 1);
        } else if (hasRecvData && Math.random() < 0.2) {
            recvCount = 1;
        }

        const tpColor = '#00ffff';

        for (let i = 0; i < sentCount; i++) {
            S.flowParticles.push(new FlowParticle(
                localPK,
                tp.remote_pk,
                0.012 + Math.random() * 0.008,
                tpColor,
                3 + Math.random() * 2
            ));
        }

        for (let i = 0; i < recvCount; i++) {
            S.flowParticles.push(new FlowParticle(
                tp.remote_pk,
                localPK,
                0.012 + Math.random() * 0.008,
                '#ff00ff',
                3 + Math.random() * 2
            ));
        }
    });

    if (S.flowParticles.length > 150) {
        setFlowParticles(S.flowParticles.slice(-150));
    }
}

function animateFlow(): void {
    const showDataflow = (document.getElementById('show-dataflow') as HTMLInputElement)?.checked;

    flowCtx.clearRect(0, 0, flowCanvas.width, flowCanvas.height);

    if (showDataflow && S.flowParticles.length > 0) {
        setFlowParticles(S.flowParticles.filter(particle => {
            const fromPos = networkToCanvas(particle.fromNode);
            const toPos = networkToCanvas(particle.toNode);

            if (fromPos && toPos) {
                particle.draw(flowCtx, fromPos, toPos);
            }

            return particle.update();
        }));
    }

    // Draw local visor glow effect
    if (S.localVisorData && S.localVisorData.connected) {
        const localPos = networkToCanvas(S.localVisorData.pub_key);
        if (localPos) {
            const glowSize = 30 + Math.sin(Date.now() / 200) * 5;
            const gradient = flowCtx.createRadialGradient(
                localPos.x, localPos.y, 0,
                localPos.x, localPos.y, glowSize
            );
            gradient.addColorStop(0, 'rgba(0, 255, 255, 0.4)');
            gradient.addColorStop(0.5, 'rgba(255, 0, 255, 0.2)');
            gradient.addColorStop(1, 'rgba(0, 255, 255, 0)');
            flowCtx.beginPath();
            flowCtx.arc(localPos.x, localPos.y, glowSize, 0, Math.PI * 2);
            flowCtx.fillStyle = gradient;
            flowCtx.fill();
        }
    }

    setFlowAnimationId(requestAnimationFrame(animateFlow));
}

export function startFlowAnimation(): void {
    flowCanvas = document.getElementById('flow-canvas') as HTMLCanvasElement;
    if (!flowCanvas) {
        console.warn('Flow canvas element not found');
        return;
    }
    flowCtx = flowCanvas.getContext('2d');
    if (!flowCtx) {
        console.warn('Failed to get 2D context for flow canvas');
        return;
    }

    resizeFlowCanvas();
    if (!S.flowAnimationId) {
        animateFlow();
    }
    setInterval(() => {
        spawnFlowParticles();
    }, 500);

    window.addEventListener('resize', resizeFlowCanvas);
}

export function clearFlowAnimation(): void {
    setFlowParticles([]);
    if (flowCtx && flowCanvas) {
        flowCtx.clearRect(0, 0, flowCanvas.width, flowCanvas.height);
    }
}
