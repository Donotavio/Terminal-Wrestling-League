package ranking

import (
	"fmt"
	"math"
)

const glickoScale = 173.7178

var (
	defaultRating = Rating{R: 1500, RD: 350, Sigma: 0.06}
	defaultConfig = Glicko2Config{Tau: 0.5, Epsilon: 1e-6, DefaultRating: defaultRating}
)

// Glicko2Config controls constants for rating updates.
type Glicko2Config struct {
	Tau           float64
	Epsilon       float64
	DefaultRating Rating
}

type glicko2Service struct {
	cfg Glicko2Config
}

func DefaultConfig() Glicko2Config {
	return defaultConfig
}

func NewGlicko2Service(cfg Glicko2Config) Service {
	if cfg.Tau == 0 {
		cfg.Tau = defaultConfig.Tau
	}
	if cfg.Epsilon == 0 {
		cfg.Epsilon = defaultConfig.Epsilon
	}
	if cfg.DefaultRating == (Rating{}) {
		cfg.DefaultRating = defaultConfig.DefaultRating
	}
	return &glicko2Service{cfg: cfg}
}

func (s *glicko2Service) Update(current Rating, opponents []OpponentResult) (Rating, error) {
	if err := validateConfig(s.cfg); err != nil {
		return Rating{}, err
	}
	if current == (Rating{}) {
		current = s.cfg.DefaultRating
	}
	if err := validateRating(current); err != nil {
		return Rating{}, err
	}
	for i, opp := range opponents {
		if err := validateRating(opp.Opponent); err != nil {
			return Rating{}, fmt.Errorf("opponent %d: %w", i, err)
		}
		if !opp.Score.IsValid() {
			return Rating{}, fmt.Errorf("opponent %d has invalid score %.2f", i, opp.Score)
		}
	}

	mu := toMu(current.R)
	phi := toPhi(current.RD)
	sigma := current.Sigma

	if len(opponents) == 0 {
		phiPrime := math.Sqrt(phi*phi + sigma*sigma)
		return normalizeRating(Rating{R: fromMu(mu), RD: fromPhi(phiPrime), Sigma: sigma}), nil
	}

	sumInvV := 0.0
	sumDelta := 0.0
	for _, opp := range opponents {
		muJ := toMu(opp.Opponent.R)
		phiJ := toPhi(opp.Opponent.RD)
		g := g(phiJ)
		e := E(mu, muJ, phiJ)
		sumInvV += g * g * e * (1 - e)
		sumDelta += g * (float64(opp.Score) - e)
	}
	if sumInvV <= 0 {
		return Rating{}, fmt.Errorf("invalid variance accumulation")
	}

	v := 1 / sumInvV
	delta := v * sumDelta
	sigmaPrime, err := solveSigmaPrime(phi, sigma, delta, v, s.cfg.Tau, s.cfg.Epsilon)
	if err != nil {
		return Rating{}, err
	}

	phiStar := math.Sqrt(phi*phi + sigmaPrime*sigmaPrime)
	phiPrime := 1 / math.Sqrt((1/(phiStar*phiStar))+(1/v))
	muPrime := mu + phiPrime*phiPrime*sumDelta

	return normalizeRating(Rating{R: fromMu(muPrime), RD: fromPhi(phiPrime), Sigma: sigmaPrime}), nil
}

// ApplySeasonSoftReset applies the agreed season transition defaults.
func ApplySeasonSoftReset(previous Rating) Rating {
	if previous == (Rating{}) {
		return defaultRating
	}
	return normalizeRating(Rating{
		R:     0.75*previous.R + 0.25*1500,
		RD:    math.Min(350, previous.RD+30),
		Sigma: previous.Sigma,
	})
}

func validateConfig(cfg Glicko2Config) error {
	if cfg.Tau <= 0 {
		return fmt.Errorf("tau must be > 0")
	}
	if cfg.Epsilon <= 0 {
		return fmt.Errorf("epsilon must be > 0")
	}
	return validateRating(cfg.DefaultRating)
}

func validateRating(r Rating) error {
	if math.IsNaN(r.R) || math.IsInf(r.R, 0) {
		return fmt.Errorf("rating R must be finite")
	}
	if r.RD <= 0 || math.IsNaN(r.RD) || math.IsInf(r.RD, 0) {
		return fmt.Errorf("rating RD must be > 0")
	}
	if r.Sigma <= 0 || math.IsNaN(r.Sigma) || math.IsInf(r.Sigma, 0) {
		return fmt.Errorf("rating sigma must be > 0")
	}
	return nil
}

func normalizeRating(r Rating) Rating {
	if r.RD < 30 {
		r.RD = 30
	}
	if r.RD > 350 {
		r.RD = 350
	}
	if r.Sigma < 0.0001 {
		r.Sigma = 0.0001
	}
	return r
}

func toMu(r float64) float64 {
	return (r - 1500) / glickoScale
}

func fromMu(mu float64) float64 {
	return mu*glickoScale + 1500
}

func toPhi(rd float64) float64 {
	return rd / glickoScale
}

func fromPhi(phi float64) float64 {
	return phi * glickoScale
}

func g(phi float64) float64 {
	return 1 / math.Sqrt(1+(3*phi*phi)/(math.Pi*math.Pi))
}

func E(mu, muJ, phiJ float64) float64 {
	return 1 / (1 + math.Exp(-g(phiJ)*(mu-muJ)))
}

func solveSigmaPrime(phi, sigma, delta, v, tau, epsilon float64) (float64, error) {
	a := math.Log(sigma * sigma)
	f := func(x float64) float64 {
		ex := math.Exp(x)
		num := ex * (delta*delta - phi*phi - v - ex)
		den := 2 * math.Pow(phi*phi+v+ex, 2)
		return num/den - (x-a)/(tau*tau)
	}

	A := a
	var B float64
	if delta*delta > phi*phi+v {
		B = math.Log(delta*delta - phi*phi - v)
	} else {
		k := 1.0
		for {
			B = a - k*tau
			if f(B) >= 0 {
				break
			}
			k++
			if k > 1000 {
				return 0, fmt.Errorf("sigma convergence precondition failed")
			}
		}
	}

	fA := f(A)
	fB := f(B)
	if math.IsNaN(fA) || math.IsNaN(fB) {
		return 0, fmt.Errorf("sigma equation produced NaN")
	}

	for math.Abs(B-A) > epsilon {
		C := A + (A-B)*fA/(fB-fA)
		fC := f(C)
		if fC*fB <= 0 {
			A = B
			fA = fB
		} else {
			fA /= 2
		}
		B = C
		fB = fC
	}

	sigmaPrime := math.Exp(A / 2)
	if sigmaPrime <= 0 || math.IsNaN(sigmaPrime) || math.IsInf(sigmaPrime, 0) {
		return 0, fmt.Errorf("invalid sigma result")
	}
	return sigmaPrime, nil
}
