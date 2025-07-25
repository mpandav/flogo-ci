/*
 * Copyright © 2017. TIBCO Software Inc.
 * This file is subject to the license terms contained
 * in the license file that is distributed with this file.
 */

package sqssendmessage

import (
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sqs"
	"github.com/project-flogo/core/activity"
	"github.com/project-flogo/core/data/coerce"
	"github.com/project-flogo/core/data/metadata"
	"github.com/project-flogo/core/support/log"
)

func init() {
	_ = activity.Register(&SQSSendMessageActivity{}, New)
}

var activityLog = log.ChildLogger(log.RootLogger(), "aws-activity-sqssendmessage")

var activityMd = activity.ToMetadata(&Settings{}, &Input{}, &Output{})

type SQSSendMessageActivity struct {
	settings *Settings
	sqssvc   *sqs.SQS
}

func New(ctx activity.InitContext) (activity.Activity, error) {
	s := &Settings{}
	err := metadata.MapToStruct(ctx.Settings(), s, true)

	if err != nil {
		return nil, err
	}

	cm, err := coerce.ToConnection(s.AWSConnection)

	if err != nil {
		return nil, err
	}

	session := cm.GetConnection().(*session.Session)
	endpointConfig := &aws.Config{}
	if session.Config.Endpoint != nil {
		endpoint := *session.Config.Endpoint
		session.Config.Endpoint = nil
		endpointConfig.Endpoint = aws.String(endpoint)
	}

	SQSSvc := sqs.New(session, endpointConfig)

	act := &SQSSendMessageActivity{settings: s, sqssvc: SQSSvc}

	return act, nil
}

func (a *SQSSendMessageActivity) Metadata() *activity.Metadata {
	return activityMd
}

func (a *SQSSendMessageActivity) Eval(context activity.Context) (done bool, err error) {

	input := &Input{}

	err = context.GetInputObject(input)
	if err != nil {
		return false, err
	}

	activityLog.Info("Executing SQS Send Message activity")

	if a.settings.QueueURL == "" {
		return false, activity.NewError("SQS Queue URL is not configured", "SQS-SENDMESSAGE-4002", nil)
	}

	if input.MessageBody == "" {
		return false, activity.NewError("Message body is not configured", "SQS-SENDMESSAGE-4003", nil)
	}

	sqsSvc := a.sqssvc
	sendMessageInput := &sqs.SendMessageInput{}
	sendMessageInput.QueueUrl = aws.String(a.settings.QueueURL)
	sendMessageInput.MessageBody = aws.String(input.MessageBody)

	messageAttributes := input.MessageAttributes
	attrsName := input.MessageAttributeNames
	if messageAttributes != nil && attrsName != nil {

		//Read mapped values
		if messageAttributes != nil {
			msgAttrs, _ := coerce.ToObject(messageAttributes)
			if msgAttrs != nil {
				//Read table values
				attrs := make(map[string]*sqs.MessageAttributeValue, len(msgAttrs))
				for _, v := range attrsName {
					attr, _ := coerce.ToObject(v)
					if attr != nil && attr["Name"] != nil && attr["Type"] != nil {
						// Has mapped value??
						if msgAttrs[attr["Name"].(string)] != nil {
							attrVal, err := coerce.ToString(msgAttrs[attr["Name"].(string)])
							if err != nil && attr["Type"].(string) == "Number" {
								attrVal, err = coerce.ToString(int(msgAttrs[attr["Name"].(string)].(int64)))
							}
							attrs[attr["Name"].(string)] = &sqs.MessageAttributeValue{
								DataType:    aws.String(attr["Type"].(string)),
								StringValue: aws.String(attrVal),
							}
						}
					}
				}
				sendMessageInput.MessageAttributes = attrs
			}
		}
	}

	if strings.HasSuffix(a.settings.QueueURL, ".fifo") {
		if input.MessageDeduplicationId != "" {
			sendMessageInput.MessageDeduplicationId = aws.String(input.MessageDeduplicationId)
		}
		sendMessageInput.MessageGroupId = aws.String(input.MessageGroupId)
	} else {
		delaySeconds := a.settings.Delay
		if delaySeconds != 0 {
			sendMessageInput.DelaySeconds = aws.Int64(int64(delaySeconds))
		}
	}

	//Send message to SQS
	response, err1 := sqsSvc.SendMessage(sendMessageInput)
	if err1 != nil {
		return false, activity.NewError(fmt.Sprintf("Failed to send message to SQS due to error:%s", err1.Error()), "SQS-SENDMESSAGE-4005", nil)
	}

	//Set Message ID in the output

	var outputObject = make(map[string]interface{})
	outputObject["MessageId"] = *response.MessageId
	if strings.HasSuffix(a.settings.QueueURL, ".fifo") {
		outputObject["SequenceNumber"] = *response.SequenceNumber
	}
	outputObjectCoerced, err := coerce.ToObject(outputObject)
	if err != nil {
		return false, err
	}
	output := &Output{}
	output.Output = outputObjectCoerced
	err = context.SetOutputObject(output)
	if err != nil {
		return false, fmt.Errorf("error setting output for Activity [%s]: %s", context.Name(), err.Error())
	}
	return true, nil
}
