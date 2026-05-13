package lexer

import "github.com/opentalon/talon-language/internal/diagnostic"

type TokenType int

const (
	// Literals
	TokenString TokenType = iota
	TokenNumber
	TokenBool
	TokenDuration

	// Delimiters
	TokenLBrace
	TokenRBrace
	TokenLBracket
	TokenRBracket
	TokenLParen
	TokenRParen
	TokenComma
	TokenDot

	// Operators
	TokenEq
	TokenNeq
	TokenLt
	TokenLte
	TokenGt
	TokenGte
	TokenPlus
	TokenMinus
	TokenStar
	TokenSlash
	TokenPercent

	// Keywords — block types
	TokenDetect
	TokenRule
	TokenRecommend
	TokenCombine
	TokenDefine
	TokenWorkflow
	TokenPredict
	TokenForecast
	TokenCluster
	TokenClassify
	TokenFind

	// Keywords — clauses
	TokenFor
	TokenWhere
	TokenWhen
	TokenAnd
	TokenOr
	TokenNot
	TokenIn
	TokenIs
	TokenHas
	TokenAttr
	TokenTypeKw
	TokenCategory
	TokenStatus
	TokenFlag
	TokenLabel
	TokenPriority
	TokenBlock
	TokenAllow
	TokenReason
	TokenAction
	TokenSuggest
	TokenReturn
	TokenBest
	TokenMinimize
	TokenMaximize
	TokenRequires
	TokenApproval
	TokenFrom
	TokenRole
	TokenBefore
	TokenAfter
	TokenEvery
	TokenOn
	TokenStep
	TokenDepends
	TokenMcp
	TokenInvoke

	// Keywords — ML
	TokenAnomaly
	TokenComparedTo
	TokenSeries
	TokenOver
	TokenWithin
	TokenSame
	TokenSimilar
	TokenCalculate
	TokenThreshold
	TokenLearnedThreshold
	TokenTrainedOn
	TokenFeatures
	TokenConfidence
	TokenClassifyKw

	// Keywords — values/units
	TokenDays
	TokenWeeks
	TokenMonths
	TokenYears
	TokenKm
	TokenLast
	TokenNext
	TokenMatching
	TokenRecords
	TokenItems
	TokenEach
	TokenToday
	TokenChangedTo

	// Keywords — string ops
	TokenContains
	TokenStartsWith
	TokenEndsWith
	TokenOlderThan
	TokenNewerThan

	// Keywords — priority values
	TokenLow
	TokenMedium
	TokenHigh
	TokenCritical

	// Keywords — misc
	TokenContext
	TokenCategoryTree

	// Special
	TokenIdent
	TokenEOF
	TokenIllegal
)

type Token struct {
	Type    TokenType
	Value   string
	Line    int
	Col     int
}

func Lex(file, source string) ([]Token, diagnostic.List) {
	panic("not implemented")
}
