"""Generate the original rate/concurrency comparison as SVG and editable draw.io."""
from prepare_assets import diagram

diagram('rate-versus-concurrency', {
    'rate': (20, 20, 240, 66, 'Arrival cap: 120 RPS', 'Rate limit permits these arrivals', False),
    'duration': (320, 20, 240, 66, 'Duration rises to 500ms', 'Previously 50ms at the same rate', False),
    'average': (620, 20, 240, 66, 'About 60 active', 'Stable average: 120 × 0.5s', False),
    'ceiling': (20, 150, 240, 66, 'Concurrency ceiling: 20', 'Example limit, chosen by operator', True),
    'active': (320, 150, 240, 66, 'At most 20 active', 'Slots held until handlers return', True),
    'reject': (620, 150, 240, 66, 'Excess arrivals: 503', 'No additional admission queue', False),
}, [('rate', 'duration', ''), ('duration', 'average', ''), ('ceiling', 'active', ''), ('active', 'reject', '')], 880, 240)
