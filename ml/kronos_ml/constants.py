"""Kronos constants, mirrored from the Go backend so the simulator matches production.

Sources:
  backend/internal/engine/physics.go   planets, SpeedOfLight, Pull_r, Time_dil, ArrivalThreshold
  backend/cmd/api/main.go              BlackHole position
  backend/internal/repository/seed.go  currencies
  backend/internal/auth/auth_service.go registration grant (1000)
"""

SPEED_OF_LIGHT = 50.0
PULL_R = 40.0           # event horizon: packet destroyed inside this radius
STALL_R = PULL_R + 20   # time-dilation zone
TIME_DIL = 0.3          # minimum dilation factor
ARRIVAL_THRESHOLD = 2.0
BLACK_HOLE = (0.0, 0.0, 100.0)

# name -> (orbit distance, angular speed rad/s, initial angle)
PLANETS = {
    "mercury": (39.0, 0.82, 0.0),
    "venus": (72.0, 0.32, 0.0),
    "earth": (100.0, 0.20, 0.0),
    "mars": (152.0, 0.11, 0.0),
    "jupiter": (520.0, 0.017, 0.0),
    "saturn": (954.0, 0.0067, 0.0),
    "uranus": (1919.0, 0.0024, 0.0),
    "neptune": (3007.0, 0.0012, 0.0),
}

# Planets that have a seeded currency, so users can actually live there.
PLANET_CURRENCY = {
    "mercury": "MERCURY",
    "venus": "VENUS",
    "earth": "EARTH",
    "mars": "MARS",
    "jupiter": "JUPITER",
    "saturn": "SATURN",
}

# Rough population split used by the simulator (inner planets are busier).
HOME_PLANET_WEIGHTS = {
    "earth": 0.30,
    "mars": 0.20,
    "venus": 0.16,
    "mercury": 0.12,
    "jupiter": 0.12,
    "saturn": 0.10,
}

REGISTRATION_GRANT = 1000.0
