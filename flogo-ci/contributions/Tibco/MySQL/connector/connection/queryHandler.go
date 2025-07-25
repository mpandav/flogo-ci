package connection

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

//DBDiagnostic ..
type DBDiagnostic struct {
	State       string `json:"State"`
	NativeError string `json:"NativeError"`
	Messge      string `json:"Message"`
}

//ActionError ..
type ActionError struct {
	APIName string         `json:"APIName"`
	Diag    []DBDiagnostic `json:"Diag,omitempty"`
}

//New ..
func New(actionerr ActionError) error {
	return &ActionError{
		APIName: actionerr.APIName,
		Diag:    actionerr.Diag,
	}
}

func (acterr *ActionError) Error() string {
	out, err := json.Marshal(acterr)
	if err != nil {
		return "Error JSON Marshal Fialed"
	}
	return string(out)
}

const alpha = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_."
const terminators = " ;),<>+-*%/"
const starters = " =,(<>+-*%/"
const quotes = "\"'`"

func alphaOnly(s string, keyset string) bool {
	for _, char := range s {
		if !strings.Contains(keyset, strings.ToLower(string(char))) {
			return false
		}
	}
	return true
}

//QueryBasicCheck ..
func QueryBasicCheck(s string) error {
	if alphaOnly(string(s[0]), quotes) || alphaOnly(string(s[len(s)-1]), quotes) {
		var dignostic []DBDiagnostic
		QueryError := ActionError{
			APIName: "HandleQuery",
			Diag: append(dignostic, DBDiagnostic{
				State:       "42000",
				NativeError: "1064",
				Messge:      "Query Syntax Error: Query should not be quoted, syntax error at or near " + string(s[0]) + " index:0",
			}),
		}
		return New(QueryError)
	}
	return nil
}

//extract the columnNames/valueFields from the paramsArray
//Since keys of the map(inputData.Values[i]) are unordered but the insertion operation in sql need column names in correct order,
//we have paramsarray which has correct order of valueFields/keys, so, we will create keys array that that all the columnNames in order
func extractKeys(paramsarray []string, inputData Input) []string {
	var keys []string
	for _, param := range paramsarray {
		if _, found := inputData.Values[0][param]; found {
			keys = append(keys, param)
		}
	}
	return keys
}

//update the insert query to append rows from the upstream array(InputData.Values) to values clause
func updateInsertQuery(query string, inputData Input, inputArgs []interface{}, paramsarray []string) (string, []interface{}) {
	re := regexp.MustCompile(`(\s|\))values(\s|\()`)
	// get index after 'values' keyword
	valuesEndIndex := re.FindStringIndex(strings.ToLower(query))[1]
	re = regexp.MustCompile(`\)(\s)*([a-z]+|;)`)
	// get ')' index of last inserted row in values clause
	index := valuesEndIndex + (re.FindStringIndex(strings.ToLower(query[valuesEndIndex:]))[0]) + 1
	value := ""
	preparedValues := ""
	keys := extractKeys(paramsarray, inputData)
	for i := 1; i < len(inputData.Values); i++ {
		for _, key := range keys {
			inputArgs = append(inputArgs, inputData.Values[i][key]) //add substitution for prepared param ($cnt) in inputArgs
			value = fmt.Sprintf("%s?,", value)
		}
		value = value[:len(value)-1]
		preparedValues = fmt.Sprintf("%s,(%s)", preparedValues, value) //preparedValues contains values to be added from input Array in insert query
		value = ""
	}
	query = fmt.Sprintf("%s%s%s", query[:index], preparedValues, query[index:])
	return query, inputArgs
}

// EvaluateQuery ... consider query:insert into users(?name, ?id) values(?name, ?id), inputData.values=[('john', 'id')]
func EvaluateQuery(query string, inputData Input) (string, []interface{}, []string, error) {
	re := regexp.MustCompile("\\n")
	query = re.ReplaceAllString(query, " ")
	re = regexp.MustCompile("\\t")
	query = re.ReplaceAllString(query, " ")
	query = strings.TrimSpace(query)
	if query[len(query)-1:] != ";" {
		query += ";"
	}
	odbcquery, param := "", ""
	endIndex := 0
	dq, sq, bt := "\"", "'", "`"
	dqm, sqm, btm := false, false, false
	var paramsarray []string
	paramMarker := false
	paramCounter := 0
	err := QueryBasicCheck(string(query))
	if err != nil {
		return "", nil, nil, err
	}
	paramIndex := 1
	prepared := query
	reducedLength := 0
	inputArgs := []interface{}{}
	for i, val := range query {
		chr := string(val)
		if !dqm || !sqm || !btm {
			if chr == dq && string(query[i-1]) != "\\" && !sqm && !btm {
				dqm = !dqm
				continue
			}
			if chr == sq && string(query[i-1]) != "\\" && !dqm && !btm {
				sqm = !sqm
				continue
			}
			if chr == bt && string(query[i-1]) != "\\" && !dqm && !sqm {
				btm = !btm
				continue
			}
			if !dqm && !sqm && !btm {
				if chr == "?" && alphaOnly(string(query[i-1]), starters) {
					if paramMarker {
						paramMarker = false
						param = ""
						continue
					}
					paramMarker = true
					param = ""
					continue
				}
				if paramMarker {
					if !alphaOnly(chr, alpha) && chr != "\n" {
						paramMarker = false
						if param == "" && string(query[i-1]) == "?" && alphaOnly(chr, terminators) {
							return "", nil, nil, fmt.Errorf("Parameters can not be unnamed, hint: ?paramname")
						}
						if param == "" && string(query[i-1]) == "?" && !alphaOnly(chr, terminators) {
							continue
						}
						if alphaOnly(chr, terminators) {
							paramsarray = append(paramsarray, param)
							paramLength := len(param)
							substituteStartIndex := i - paramLength - 1 - reducedLength
							substitute := "?"
							//substitution of paramIndex-> insert into users(?name, ?id) values($1, ?id)
							prepared = prepared[:substituteStartIndex] + substitute + prepared[i-reducedLength:]
							paramIndex++
							reducedLength += paramLength - len(substitute) + 1

							substitution, error := getSubstutionValue(param, inputData)
							if error != nil {
								return "", nil, nil, error
							}
							// contains actual values :'john', 'id' mapped with $index in prepared query
							inputArgs = append(inputArgs, substitution)

							odbcquery += query[endIndex : i-len(param)]
							endIndex = i
							paramCounter++
							param = ""
							continue
						}
					}
					param = param + chr
				}
			}
		}
	}
	logCache.Debug("ParamsArray: ", paramsarray)
	//check if the data passed is upstream array passed using forEach , refer WIPGRS-463
	if strings.ToLower(strings.Split(query, " ")[0]) == "insert" {
		//inputData.Values always contains entire rows when passed through
		if len(inputData.Values) > 1 {
			prepared, inputArgs = updateInsertQuery(prepared, inputData, inputArgs, paramsarray)
		}
	}
	inputArgs = formatNullValues(inputArgs)
	odbcquery += query[endIndex:]
	return prepared, inputArgs, paramsarray, nil
}

//refer WIMYSQ-546
func formatNullValues(inputArgs []interface{}) []interface{} {
	for i := 0; i < len(inputArgs); i++ {
		if inputArgs[i] == "null" || inputArgs[i] == "" {
			inputArgs[i] = nil
		}
	}
	return inputArgs
}

//ex- will extract 'john' from inputdata.Values or inputdata.Parameters for parameter 'name'
func getSubstutionValue(parameter string, inputData Input) (interface{}, error) {
	substitution, ok := inputData.Parameters[parameter]
	if !ok {
		for _, values := range inputData.Values {
			substitution, ok = values[parameter]
			if ok {
				break
			}
		}
		if !ok {
			return nil, fmt.Errorf("missing substitution for: %s", parameter)
		}
	}
	// parameterType, ok := schema[parameter]
	// if ok && parameterType == "BYTEA" {
	// 	substitution = decodeBlob(substitution.(string))
	// }

	return substitution, nil
}
